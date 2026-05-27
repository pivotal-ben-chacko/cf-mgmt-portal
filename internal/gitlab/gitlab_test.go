package gitlab

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, Token: "tkn", HTTP: srv.Client()}
}

func TestGetFile_PathEscapesProjectAndFile(t *testing.T) {
	var gotURI, gotToken string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotURI = r.RequestURI
		gotToken = r.Header.Get("PRIVATE-TOKEN")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "hello")
	})
	b, err := c.GetFile(context.Background(), "Global/ofs-lowers/fog-cf-mgmt", "main", "fog/orgs.yml")
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello" {
		t.Errorf("body: %q", b)
	}
	if gotToken != "tkn" {
		t.Errorf("PRIVATE-TOKEN: %q", gotToken)
	}
	want := "/api/v4/projects/Global%2Fofs-lowers%2Ffog-cf-mgmt/repository/files/fog%2Forgs.yml/raw?ref=main"
	if gotURI != want {
		t.Errorf("uri:\n  got  %s\n  want %s", gotURI, want)
	}
}

func TestCreateBranch_SendsPOSTWithBranchAndRef(t *testing.T) {
	var gotURI string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: %s", r.Method)
		}
		gotURI = r.RequestURI
		w.WriteHeader(http.StatusCreated)
	})
	if err := c.CreateBranch(context.Background(), "P", "feature/x", "main"); err != nil {
		t.Fatal(err)
	}
	want := "/api/v4/projects/P/repository/branches?branch=feature%2Fx&ref=main"
	if gotURI != want {
		t.Errorf("uri:\n  got  %s\n  want %s", gotURI, want)
	}
}

func TestCommitFiles_PostsActionsAsJSON(t *testing.T) {
	var got struct {
		Branch        string         `json:"branch"`
		CommitMessage string         `json:"commit_message"`
		Actions       []commitAction `json:"actions"`
	}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type: %q", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.WriteHeader(http.StatusCreated)
	})
	err := c.CommitFiles(context.Background(), "P", "br", "msg", []CommitAction{
		{Action: "update", FilePath: "f.yml", Content: "x: 1"},
		{Action: "create", FilePath: "g.json", Content: "[]"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "br" || got.CommitMessage != "msg" {
		t.Errorf("body: %+v", got)
	}
	if len(got.Actions) != 2 || got.Actions[1].Action != "create" {
		t.Errorf("actions: %+v", got.Actions)
	}
}

func TestCommitFiles_NoActionsIsError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not be called")
	})
	if err := c.CommitFiles(context.Background(), "P", "br", "m", nil); err == nil {
		t.Fatal("expected error for empty actions")
	}
}

func TestOpenMR_ReturnsWebURL(t *testing.T) {
	var got struct {
		SourceBranch string `json:"source_branch"`
		AssigneeIDs  []int  `json:"assignee_ids"`
	}
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"web_url":"https://gitlab.example.com/p/-/merge_requests/42"}`)
	})
	u, err := c.OpenMR(context.Background(), "P", OpenMRRequest{
		SourceBranch: "feat",
		TargetBranch: "main",
		Title:        "t",
		AssigneeIDs:  []int{7, 8},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(u, "/42") {
		t.Errorf("web_url: %s", u)
	}
	if got.SourceBranch != "feat" || len(got.AssigneeIDs) != 2 {
		t.Errorf("body: %+v", got)
	}
}

func TestGroupMembers_Paginates(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("page") {
		case "1":
			w.Header().Set("X-Next-Page", "2")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `[{"id":1},{"id":2}]`)
		case "2":
			w.Header().Set("X-Next-Page", "")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `[{"id":3}]`)
		default:
			t.Fatalf("unexpected page: %q", r.URL.Query().Get("page"))
		}
	})
	ids, err := c.GroupMembers(context.Background(), "platform-cf-admins")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != 1 || ids[2] != 3 {
		t.Errorf("ids: %v", ids)
	}
}

func TestErrorResponse_IncludesStatusAndBody(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"404 Not Found"}`)
	})
	_, err := c.GetFile(context.Background(), "P", "main", "x")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("missing 404 in error: %v", err)
	}
	if !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("missing body in error: %v", err)
	}
}
