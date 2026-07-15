# cf-mgmt-portal

A self-service web portal that lets Cloud Foundry **org managers** edit
[`cf-mgmt`](https://github.com/vmwarepivotallabs/cf-mgmt) YAML configuration —
add a user to a space role, create a space — without direct write access to the
config repo.

**The portal never mutates CF or the config repo directly.** Every action opens
a **GitLab merge request** against the config repo for platform-team review;
changes take effect only after the MR is merged and the downstream `cf-mgmt`
pipeline runs.

## How it works

A mutating action flows through four stages (`internal/http/handlers.go`):

1. **Authn** — user logs in via **UAA OAuth** (UAA is LDAP-integrated, so the
   `user_name` claim equals the `sAMAccountName` used in `spaceConfig.yml`;
   credentials are entered on UAA's login page, never the portal's).
   Sessions are stateless, HMAC-signed cookies.
2. **Authz** — before any write, the portal checks via the **CF API** (using a
   read-only UAA service account) that the user holds the required role
   (e.g. OrgManager) on the target org.
3. **Mutate** — `internal/mutate` performs the pure YAML transform (read bytes →
   cf-mgmt config structs → modify → re-marshal).
4. **Persist** — `internal/gitlab` branches, commits, and opens the MR, assigning
   the platform team as reviewers.

## Configuration

All runtime config is environment variables (see `manifest.yml`; every value is
required at startup or the app exits). They fall into three groups:

| Group | Vars | Setup guide |
|---|---|---|
| Portal session | `PORTAL_URL`, `SESSION_KEY`, `FOUNDATION`, `TARGET_BRANCH` | [docs/gitlab-setup.md](docs/gitlab-setup.md#session_key--what-it-is-and-how-to-create-it) |
| GitLab (config repo) | `GITLAB_URL`, `GITLAB_TOKEN`, `CONFIG_REPO_PROJECT`, `PLATFORM_TEAM_GROUP` | [docs/gitlab-setup.md](docs/gitlab-setup.md) |
| UAA login | `UAA_LOGIN_CLIENT_ID`/`_SECRET` | [docs/cf-uaa-setup.md](docs/cf-uaa-setup.md) |
| CF authz | `CF_API_URL`, `UAA_URL`, `UAA_CLIENT_ID`/`_SECRET` | [docs/cf-uaa-setup.md](docs/cf-uaa-setup.md) |

The setup guides cover how to create each GitLab/CF object and retrieve its
value, the login flow, and both UAA clients. `manifest.yml` holds only
`CHANGE-ME` placeholders for secrets — set real values with
`cf set-env cf-mgmt-portal <VAR> <value>` and restage.

### UAA clients

Login and authorization use **two separate UAA clients** (create both once per
foundation; full walkthrough in [docs/cf-uaa-setup.md](docs/cf-uaa-setup.md)):

- **`cf-mgmt-portal-login`** (`UAA_LOGIN_CLIENT_*`) — user sign-in via the
  authorization-code flow. Users authenticate on UAA's own login page; the
  `user_name` claim from `/userinfo` is the LDAP `sAMAccountName` the portal
  uses everywhere. No CF API authorities.

  ```bash
  uaac target https://uaa.system.<domain>
  uaac token client get admin -s <UAA_ADMIN_CLIENT_SECRET>
  uaac client add cf-mgmt-portal-login \
    --name "cf-mgmt-portal user login" \
    --authorized_grant_types authorization_code,refresh_token \
    --scope openid \
    --autoapprove openid \
    --redirect_uri "<PORTAL_URL>/auth/callback" \
    --secret <CHOOSE_A_STRONG_SECRET>
  ```

  The `--redirect_uri` must equal `$PORTAL_URL/auth/callback` **exactly**
  (scheme included), and a login client takes `--scope`, not `--authorities`.

- **`cf-mgmt-portal`** (`UAA_CLIENT_*`) — read-only `client_credentials`
  service account (`--authorities cloud_controller.admin_read_only`) used to
  check the logged-in user's CF org role before any action. It authorizes; it
  never writes.

## Develop

```bash
go build ./cmd/portal      # build
go test ./...              # all tests
go vet ./...

# Run locally (export the manifest.yml env vars first; listens on $PORT, default 8080):
go run ./cmd/portal
```

The `cf-mgmt` dependency is wired via a local `replace` directive in `go.mod`
(it reuses cf-mgmt's `config` structs so emitted YAML round-trips through the
real pipeline); `vendor/` is committed.

## Deploy

```bash
cf push   # uses manifest.yml + the Go buildpack
```

