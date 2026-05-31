# GitLab setup for cf-mgmt-portal

How to create the GitLab objects the portal needs and retrieve the values that
go into `manifest.yml` (the `GITLAB_*`, `CONFIG_REPO_PROJECT`, and
`PLATFORM_TEAM_GROUP` env vars). Do this once per GitLab instance.

The portal uses GitLab two ways:

- **User login** — via an **OAuth application** (`GITLAB_OAUTH_CLIENT_ID` /
  `_SECRET`). GitLab is the IdP; the GitLab username must equal the user's
  LDAP `sAMAccountName` (e.g. `F920U2K`).
- **Repo writes** — via a **personal/project access token** (`GITLAB_TOKEN`,
  `api` scope) used to read files, push branches, and open MRs against the
  config repo.

## What you'll create → which env var it produces

| Object | Example value | Env var |
|---|---|---|
| Instance base URL | `http://gitlab.skynetsystems.io:8929` | `GITLAB_URL` |
| OAuth application | `cf-mgmt-portal` | `GITLAB_OAUTH_CLIENT_ID` / `_SECRET` |
| Access token (`api` scope) | `glpat-…` | `GITLAB_TOKEN` |
| Config repo project | `Global/ofs-lowers/fog-cf-mgmt` | `CONFIG_REPO_PROJECT` |
| Assignee group | `platform-cf-admins` | `PLATFORM_TEAM_GROUP` |

> The live values for this foundation are kept in `manifest.yml`. The OAuth
> secret (`gloas-…`) and access token (`glpat-…`) are **shown only once** at
> creation — copy them immediately; if lost, regenerate (you can't read them
> back).

---

## Prerequisites

- A reachable GitLab instance and an account with **Admin** access (needed for
  the instance-wide OAuth application). Here: `http://gitlab.skynetsystems.io:8929`,
  user `root`.
- Decide the **portal's own URL** (`PORTAL_URL`) up front — the OAuth redirect
  URI must match it exactly. For Cloud Foundry it's the app route, e.g.
  `https://cf-mgmt-portal.apps.skynetsystems.io`; for local runs it's
  `http://localhost:8080`.

---

## 1. `GITLAB_URL`

The base URL of your instance, including a non-standard port if there is one.
No trailing slash. No TLS is required by the portal.

```
GITLAB_URL: http://gitlab.skynetsystems.io:8929
```

Whatever value you use must be reachable **from wherever the portal runs**
(from the CF foundation, not just your laptop) for the server-side token
exchange, and from the **user's browser** for the login redirect.

## 2. Config repo project → `CONFIG_REPO_PROJECT`

This is the GitLab project that holds the cf-mgmt config the portal edits. The
example path `Global/ofs-lowers/fog-cf-mgmt` is group → subgroup → project.

1. Top bar **+ → New group**, name `Global` → **Create group**.
2. Inside `Global`, **New subgroup**, name `ofs-lowers`.
3. Inside `ofs-lowers`, **New project → Create blank project**, name
   `fog-cf-mgmt`, tick **Initialize repository with a README** → **Create**.
4. `CONFIG_REPO_PROJECT` is the full path: `Global/ofs-lowers/fog-cf-mgmt`
   (pass the human path; the portal URL-encodes it).
5. **Create the target branch.** The portal opens MRs against `TARGET_BRANCH`
   (default `development`). In the project: **Code → Branches → New branch**,
   name `development`, from `main`.
6. **Seed the foundation layout** so post-login actions have files to read. The
   portal expects, under the `FOUNDATION` subdir (`fog`):

   ```
   fog/<org>/spaces.yml                 # read by "create space"
   fog/<org>/<space>/spaceConfig.yml    # read by "add user to role"
   fog/<org>/<space>/security-group.json
   fog/spaceDefaults.yml                # optional defaults for new spaces
   ```

   Add these on the `development` branch (**Code → Repository → `+` → New file**,
   or **Web IDE**). A minimal `fog/<org>/spaces.yml`:

   ```yaml
   org: <org>
   spaces:
   - <space>
   enable-delete-spaces: false
   ```

   and `fog/<org>/<space>/spaceConfig.yml`:

   ```yaml
   org: <org>
   space: <space>
   space-developer:  {ldap_users: [], users: [], saml_users: [], ldap_groups: []}
   space-manager:    {ldap_users: [], users: [], saml_users: [], ldap_groups: []}
   space-auditor:    {ldap_users: [], users: [], saml_users: [], ldap_groups: []}
   space-supporter:  {ldap_users: [], users: [], saml_users: [], ldap_groups: []}
   allow-ssh: true
   ```

   `<org>` must be a **real CF org** for actions to pass the authz check (see
   the portal's CF/UAA setup) — `demo-org` is only placeholder content.

## 3. Assignee group → `PLATFORM_TEAM_GROUP`

The group whose members are auto-assigned to the MRs the portal opens.

1. **+ → New group**, name `platform-cf-admins` → **Create group**.
2. **Manage → Members → Invite members**; add at least one user (so there's an
   assignee).
3. `PLATFORM_TEAM_GROUP` is the group path: `platform-cf-admins`.

## 4. OAuth application → `GITLAB_OAUTH_CLIENT_ID` / `_SECRET`

For user login. Use an **instance-wide** application (Admin) so it isn't tied
to one user's account.

1. **Admin Area** (`/admin`) **→ Applications → New application**.
   (For a user-owned app instead: avatar **→ Edit profile → Applications**,
   `/-/user_settings/applications`.)
2. **Name:** `cf-mgmt-portal`
3. **Redirect URI:** must equal `PORTAL_URL` + `/auth/callback`. You can list
   several, one per line, to support both CF and local:
   ```
   https://cf-mgmt-portal.apps.skynetsystems.io/auth/callback
   http://localhost:8080/auth/callback
   ```
4. **Confidential:** checked (the portal sends a client secret).
5. **Scopes:** check **`read_user`** only.
6. **Save application.** Copy the **Application ID** → `GITLAB_OAUTH_CLIENT_ID`
   and the **Secret** (`gloas-…`) → `GITLAB_OAUTH_CLIENT_SECRET` now; the secret
   is shown once.

## 5. Access token → `GITLAB_TOKEN`

For repo writes. A **personal access token** is simplest; a **project access
token** scoped to just the config project is tighter.

1. Avatar **→ Edit profile → Access Tokens**
   (`/-/user_settings/personal_access_tokens`).
2. **Name:** `cf-mgmt-portal`, set an expiry.
3. **Scopes:** check **`api`**.
4. **Create personal access token** → copy the `glpat-…` value →
   `GITLAB_TOKEN`. Shown once.

The account that owns this token must have at least **Developer** (to push
branches/commits) and the ability to open MRs on `CONFIG_REPO_PROJECT`.

---

## How the OAuth login flow works

The portal uses the standard **OAuth 2.0 authorization-code** flow with GitLab
as the identity provider. It only ever uses OAuth to learn *who you are* — it
does **not** act on GitLab as you (repo writes use the separate `GITLAB_TOKEN`).

```
Browser                         Portal (CF)                     GitLab
   |  GET /auth/login              |                              |
   |-----------------------------> |                              |
   |   302 to GitLab /authorize    | mint random `state`,         |
   |   (+ Set-Cookie: state)       | stash it in a 5-min cookie   |
   | <-----------------------------|                              |
   |  GET /oauth/authorize?client_id&redirect_uri&scope=read_user&state           |
   |---------------------------------------------------------------------------->  |
   |                               |   user logs in / approves    |
   |  302 to /auth/callback?code=…&state=…                        |               |
   | <----------------------------------------------------------------------------|
   |  GET /auth/callback?code&state |                             |
   |-----------------------------> | 1. verify state == cookie    |
   |                               | 2. POST /oauth/token  -------> exchange code  |
   |                               |    (back-channel, w/ secret) <--- access tok  |
   |                               | 3. GET /api/v4/user   -------> who am I?      |
   |                               |                       <--- id/username/email  |
   |                               | 4. build + SIGN session,     |
   |   302 to /  (+ Set-Cookie: session)  discard the GitLab token|
   | <-----------------------------|                              |
```

Step by step (see `internal/http/handlers.go` and `internal/auth/auth.go`):

1. **`GET /auth/login`** — the portal generates a random **`state`** value,
   stores it in a short-lived (5 min), `HttpOnly`, `Secure` cookie scoped to
   `/auth/callback`, then 302-redirects the browser to GitLab's
   `/oauth/authorize` with `client_id`, `redirect_uri`, `response_type=code`,
   `scope=read_user`, and that `state`.
2. **GitLab authenticates** the user (and, the first time, asks them to
   authorize the app), then 302-redirects the browser back to the registered
   **redirect URI** (`PORTAL_URL/auth/callback`) with `?code=…&state=…`.
3. **`GET /auth/callback`** — the portal first checks the returned `state`
   equals the value in the `state` cookie. This is **CSRF / code-injection
   protection**; a missing or mismatched `state` is rejected with "invalid
   state". This is *why the redirect must be over HTTPS in CF* — the `state`
   cookie is `Secure`, so a plain-HTTP callback wouldn't send it back.
4. **Back-channel token exchange** — server-side (never in the browser), the
   portal `POST`s the `code` to GitLab's `/oauth/token` **with the client
   secret** and gets an access token.
5. **Identity lookup** — it calls `GET /api/v4/user` with that token to read
   `id`, `username`, and `email`.
6. **Session minted** — the portal builds a session (`username`, `email`,
   GitLab id, an 8-hour expiry, and a per-session CSRF token), **signs it with
   `SESSION_KEY`**, and sets it as the session cookie. The **GitLab access
   token is then discarded** — it's only used for the one user lookup.
7. The browser is redirected to `/`, now authenticated. Every later request
   carries the signed session cookie, which `requireAuth` verifies before
   running an action.

Why `read_user` and nothing more: the portal needs only the **username**, which
(because GitLab is LDAP-synced) equals the user's `sAMAccountName` — the same
identity used in `spaceConfig.yml` and checked against CF for OrgManager. It
never needs `api` access *as the user*.

---

## `SESSION_KEY` — what it is and how to create it

`SESSION_KEY` is the secret **HMAC-SHA256 signing key** for the portal's
session cookie. The portal keeps **no server-side session store** — the cookie
itself carries the identity, and the signature is what makes it trustworthy
(`internal/http/session.go`). The cookie looks like:

```
cf_mgmt_portal_session = base64url(json-payload) "." base64url(HMAC-SHA256(SESSION_KEY, base64url(json-payload)))
```

On every authenticated request the portal recomputes the HMAC with
`SESSION_KEY` and compares it (constant-time) to the signature in the cookie.

### Why it's needed

Without a signature, the session cookie would be **user-editable**. An attacker
could change the `username` field to that of an OrgManager and walk straight
past the portal's authorization check (which keys off `sess.Username`). The HMAC
makes forging a valid cookie infeasible without knowing `SESSION_KEY` — so it's
the single secret that underpins the integrity of *every* logged-in session.

Two consequences to keep in mind:

- **Signed, not encrypted.** The payload is base64 — anyone can *read* it. It
  holds only username/email/GitLab-id (not secrets), which is fine; just never
  put anything sensitive in the session.
- **Keep it secret and stable.** Anyone with the key can mint a valid session
  for any user, so guard it like the GitLab token (and don't leave it in the
  git-tracked manifest for anything beyond throwaway dev). **Changing the key
  invalidates every existing cookie**, forcing all users to log in again —
  which is exactly how you'd force a global logout or respond to a leak.

### Requirements

- The portal **refuses to start** if `SESSION_KEY` is shorter than 32
  characters (`cmd/portal/main.go`).
- It must be **random**, not a password or passphrase. For HMAC-SHA256 aim for
  at least 32 bytes (256 bits) of entropy.

### How to generate one

Any of these produce a suitable value:

```bash
openssl rand -base64 32
# or
python3 -c "import secrets; print(secrets.token_urlsafe(32))"
# or
head -c 32 /dev/urandom | base64
```

Then set it (keep it out of git for real deployments):

```bash
cf set-env cf-mgmt-portal SESSION_KEY "$(openssl rand -base64 32)"
cf restart cf-mgmt-portal
```

---

## Appendix: doing it via API / CLI

The same objects, scripted. Bootstrap an admin token from inside the container
(no UI), then use the REST API. Requires the `gitlab` container running.

```bash
# 1. Mint an api-scoped admin token for root via the Rails console
docker exec -i gitlab gitlab-rails runner - <<'RUBY'
u = User.find_by_username('root')
t = u.personal_access_tokens.create!(name: 'bootstrap', scopes: ['api'],
                                     expires_at: 300.days.from_now)
puts t.token   # -> use as PRIVATE-TOKEN below
RUBY

G=http://localhost:8929/api/v4
H="PRIVATE-TOKEN: <token-from-above>"

# 2. Groups + subgroup + project (capture the returned ids)
curl -s -H "$H" -X POST "$G/groups" --data-urlencode name=Global --data-urlencode path=Global -d visibility=private
curl -s -H "$H" -X POST "$G/groups" --data-urlencode name=ofs-lowers --data-urlencode path=ofs-lowers -d parent_id=<GLOBAL_ID> -d visibility=private
curl -s -H "$H" -X POST "$G/projects" --data-urlencode name=fog-cf-mgmt --data-urlencode path=fog-cf-mgmt -d namespace_id=<SUBGROUP_ID> -d initialize_with_readme=true -d visibility=private
curl -s -H "$H" -X POST "$G/groups" --data-urlencode name=platform-cf-admins --data-urlencode path=platform-cf-admins -d visibility=private

# 3. development branch
curl -s -H "$H" -X POST "$G/projects/<PROJECT_ID>/repository/branches?branch=development&ref=main"

# 4. Instance-wide OAuth app (admin token required) -> application_id + secret
curl -s -H "$H" -X POST "$G/applications" \
  --data-urlencode name=cf-mgmt-portal \
  --data-urlencode 'redirect_uri=https://cf-mgmt-portal.apps.skynetsystems.io/auth/callback
http://localhost:8080/auth/callback' \
  --data-urlencode scopes=read_user -d confidential=true

# 5. The bootstrap token from step 1 (api scope) is your GITLAB_TOKEN,
#    or mint a dedicated one the same way.
```

Commit files (e.g. the `fog/` seed) in one shot with the commits API:

```bash
curl -s -H "$H" -H 'Content-Type: application/json' \
  -X POST "$G/projects/<PROJECT_ID>/repository/commits" -d '{
    "branch": "development",
    "commit_message": "seed fog foundation",
    "actions": [
      {"action":"create","file_path":"fog/demo-org/spaces.yml","content":"org: demo-org\nspaces:\n- dev\n"}
    ]
  }'
```
