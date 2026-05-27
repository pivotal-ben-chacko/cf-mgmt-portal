package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client is a minimal GitLab v4 REST client scoped to what the portal needs:
// read a file, branch off main, commit, open an MR, and resolve a group's
// current members for assignee IDs.
//
// Authentication uses the PRIVATE-TOKEN header, which accepts personal,
// project, and group access tokens. The token needs `api` scope.
//
// Project identifiers are passed in their human form (e.g.
// "Global/ofs-lowers/fog-cf-mgmt") and URL-encoded internally.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

type CommitAction struct {
	Action   string // "create" | "update" | "delete"
	FilePath string
	Content  string
}

type OpenMRRequest struct {
	SourceBranch string
	TargetBranch string
	Title        string
	Description  string
	AssigneeIDs  []int
}

func (c *Client) GetFile(ctx context.Context, project, ref, path string) ([]byte, error) {
	u := fmt.Sprintf("/api/v4/projects/%s/repository/files/%s/raw?ref=%s",
		url.PathEscape(project),
		url.PathEscape(path),
		url.QueryEscape(ref),
	)
	resp, err := c.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errResp(resp)
	}
	return io.ReadAll(resp.Body)
}

func (c *Client) CreateBranch(ctx context.Context, project, name, source string) error {
	u := fmt.Sprintf("/api/v4/projects/%s/repository/branches?branch=%s&ref=%s",
		url.PathEscape(project),
		url.QueryEscape(name),
		url.QueryEscape(source),
	)
	resp, err := c.do(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return errResp(resp)
	}
	return nil
}

func (c *Client) CommitFiles(ctx context.Context, project, branch, message string, actions []CommitAction) error {
	if len(actions) == 0 {
		return fmt.Errorf("gitlab: CommitFiles called with no actions")
	}
	body := struct {
		Branch        string         `json:"branch"`
		CommitMessage string         `json:"commit_message"`
		Actions       []commitAction `json:"actions"`
	}{Branch: branch, CommitMessage: message}
	for _, a := range actions {
		body.Actions = append(body.Actions, commitAction{
			Action:   a.Action,
			FilePath: a.FilePath,
			Content:  a.Content,
		})
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	u := fmt.Sprintf("/api/v4/projects/%s/repository/commits", url.PathEscape(project))
	resp, err := c.do(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return errResp(resp)
	}
	return nil
}

func (c *Client) OpenMR(ctx context.Context, project string, req OpenMRRequest) (string, error) {
	body := struct {
		SourceBranch string `json:"source_branch"`
		TargetBranch string `json:"target_branch"`
		Title        string `json:"title"`
		Description  string `json:"description,omitempty"`
		AssigneeIDs  []int  `json:"assignee_ids,omitempty"`
	}{
		SourceBranch: req.SourceBranch,
		TargetBranch: req.TargetBranch,
		Title:        req.Title,
		Description:  req.Description,
		AssigneeIDs:  req.AssigneeIDs,
	}
	b, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	u := fmt.Sprintf("/api/v4/projects/%s/merge_requests", url.PathEscape(project))
	resp, err := c.do(ctx, http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", errResp(resp)
	}
	var mr struct {
		WebURL string `json:"web_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&mr); err != nil {
		return "", fmt.Errorf("gitlab: decode MR response: %w", err)
	}
	return mr.WebURL, nil
}

// GroupMembers returns user IDs of all current members of the group, including
// inherited members from parent groups (uses /members/all).
func (c *Client) GroupMembers(ctx context.Context, group string) ([]int, error) {
	var ids []int
	page := 1
	for {
		u := fmt.Sprintf("/api/v4/groups/%s/members/all?per_page=100&page=%d",
			url.PathEscape(group), page)
		resp, err := c.do(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, errResp(resp)
		}
		var members []struct {
			ID int `json:"id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&members); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("gitlab: decode members: %w", err)
		}
		next := resp.Header.Get("X-Next-Page")
		resp.Body.Close()

		for _, m := range members {
			ids = append(ids, m.ID)
		}
		if next == "" {
			return ids, nil
		}
		n, err := strconv.Atoi(next)
		if err != nil {
			return nil, fmt.Errorf("gitlab: bad X-Next-Page %q: %w", next, err)
		}
		page = n
	}
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.HTTP.Do(req)
}

func errResp(r *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<10))
	return fmt.Errorf("gitlab %s %s: status %d: %s",
		r.Request.Method, r.Request.URL.Path, r.StatusCode, bytes.TrimSpace(body))
}

type commitAction struct {
	Action   string `json:"action"`
	FilePath string `json:"file_path"`
	Content  string `json:"content,omitempty"`
}
