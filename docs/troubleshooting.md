# Troubleshooting cf-mgmt-portal

Field-tested diagnostics for the failures you're most likely to hit when
standing the portal up against a new environment (e.g. moving from the local
dev stack to production). Ordered roughly by where they occur in the request
flow: login → GitLab reads → CF authz.

## The golden rule: dev credentials don't transfer

Every credential in `manifest.yml` is minted **per system**:

| Credential | Minted on | Production needs |
|---|---|---|
| `GITLAB_OAUTH_CLIENT_ID/SECRET` | one GitLab instance | a new OAuth app registered on the prod GitLab |
| `GITLAB_TOKEN` (PAT) | one GitLab instance | a new PAT/bot token with membership on the prod config repo |
| `UAA_CLIENT_ID/SECRET` | one CF foundation's UAA | a new UAA client created on the prod UAA |

None of them work anywhere but the system that issued them. When something
auth-shaped fails right after an environment change, assume a stale credential
before anything else.

Also: `cf set-env` does **nothing** until you `cf restage` (or `cf restart`)
the app. "I fixed the env var but it still fails" is almost always this.

```bash
cf env cf-mgmt-portal        # what the app REALLY has (not what the manifest says)
cf restage cf-mgmt-portal    # apply env changes
```

---

## 1. OAuth login: "Client authentication failed due to unknown client…"

> **Note:** login has since moved from GitLab OAuth to **UAA** (same
> authorization-code flow, `UAA_LOGIN_CLIENT_*` env vars, client created per
> `docs/cf-uaa-setup.md`). UAA phrases this error differently
> (`invalid_client` / "Bad credentials"), but every diagnostic below applies
> unchanged — substitute `$UAA_URL` for `$GITLAB_URL` and the UAA login
> client for the OAuth app.

This is the IdP's `invalid_client`: the instance doesn't recognize
the `client_id`/`client_secret` pair the portal sent.

### Step 1 — where does the error appear?

- **On a GitLab-rendered page immediately after clicking "Login with
  GitLab"** → the authorize step only validates `client_id`, so the ID itself
  is unknown to that GitLab. The secret isn't involved yet.
- **Back on the portal's failure page (`oauth exchange: …`)** → the ID was
  accepted; the `client_secret` failed at the `POST /oauth/token` exchange.

### Step 2 — read the browser address bar

When the portal redirects to GitLab, the URL is
`<GITLAB_URL>/oauth/authorize?client_id=…`. On the error page, check:

1. **The host** — is it the GitLab you think it is, or a stale `GITLAB_URL`?
2. **The `client_id` param** — compare character-for-character against the
   Application ID on that instance's Applications page. Watch for an encoded
   newline (`%0A`) or space at the end (quoting artifacts from `cf set-env`).

### Step 3 — differential curl test

Send a deliberately bogus code and read which error comes back:

```bash
curl -s -X POST "$GITLAB_URL/oauth/token" \
  -d "client_id=$CLIENT_ID" \
  -d "client_secret=$CLIENT_SECRET" \
  -d "grant_type=authorization_code" \
  -d "code=bogus" \
  -d "redirect_uri=$PORTAL_URL/auth/callback"
```

- `invalid_client` → the ID/secret pair is wrong **for this instance**.
- `invalid_grant` → the credentials are **fine** (client auth passed; only the
  fake code was rejected). The running app must be sending different values
  than you just tested — go back to `cf env` / restage.

### Common root causes

- OAuth app was registered on a different GitLab instance than `GITLAB_URL`
  points at (apps are per-instance; register a new one: Settings →
  Applications, redirect URI `<PORTAL_URL>/auth/callback`, scope `read_user`).
- Env values updated but the app never restaged.
- Secret was **regenerated** in GitLab's UI — the old one dies instantly, and
  the new one is shown only once.
- App created with **"Confidential" unchecked** — the portal expects a
  confidential app that authenticates with a secret.
- Literal quotes or a trailing newline in the env value (`cat -A` makes
  whitespace visible).

---

## 2. GitLab API: `404 Project Not Found`

Example log line:

```
gitlab GET /api/v4/projects/<group-path>/repository/files/fog/orgs.yml/raw: status 404
```

### Step 1 — read the URL split

Everything between `/projects/` and `/repository/files/` is what the portal
thinks the project is (`CONFIG_REPO_PROJECT`); everything after
`/repository/files/` is the file path inside the repo
(`<FOUNDATION>/orgs.yml`). If the project segment ends at a **group** (e.g.
`…/ofs-lowers` with no repo name), `CONFIG_REPO_PROJECT` is set to the group
path instead of the full project path.

The two similarly-shaped vars are easy to cross:

- `CONFIG_REPO_PROJECT` — **full project path**, everything after the host in
  the clone URL, minus `.git`:
  `global/corp/tech-svcs/cloud-foundry/na-cf-mgmt/ofs-lowers/fog-cf-mgmt`
- `PLATFORM_TEAM_GROUP` — a **group** path whose members become MR assignees.
  Pick the narrowest group that is actually the platform team: the lookup
  uses `/members/all`, which includes members inherited from parent groups, so
  pointing it at a broad ancestor group assigns the MR to a huge list.
- `GITLAB_URL` — scheme + host only (`https://gitlab.onefiserv.net`), no
  group path, no `.git`.

### Step 2 — if the path is right, it's permissions

GitLab deliberately returns **404 (not 403)** for projects a token can't see.
Give the `GITLAB_TOKEN`'s user/bot membership on the project — Reporter is
enough to read, but the portal also branches/commits/opens MRs, so it needs
**Developer**. Verify directly:

```bash
curl -s -H "PRIVATE-TOKEN: $GITLAB_TOKEN" \
  "$GITLAB_URL/api/v4/projects/$(python3 -c 'import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1],safe=""))' "$CONFIG_REPO_PROJECT")" \
  | python3 -m json.tool | head
```

404 here with a correct path = the token can't see the project.

---

## 3. CF authz: "invalid client credentials" when checking an org

The portal's first act on any mutation is a `client_credentials` token grab
from `$UAA_URL/oauth/token` (see `internal/cfapi`). This error means that UAA
rejected `UAA_CLIENT_ID`/`UAA_CLIENT_SECRET`. UAA clients are
**per-foundation** — the client created on the dev foundation doesn't exist
in production.

### Step 1 — confirm the env points at the right foundation

```bash
cf env cf-mgmt-portal | grep -E "CF_API_URL|UAA_URL|UAA_CLIENT_ID"
# the correct UAA URL is discoverable from the CF API root:
curl -s "$CF_API_URL/" | python3 -m json.tool | grep -A2 '"uaa"'
```

### Step 2 — differential curl against that UAA

```bash
curl -s -X POST "$UAA_URL/oauth/token" \
  -d grant_type=client_credentials \
  -d client_id=cf-mgmt-portal \
  --data-urlencode "client_secret=$UAA_CLIENT_SECRET"
```

- `access_token` in the response → credentials fine; the running app has
  different values (restage).
- `invalid_client` → the client doesn't exist on this UAA or the secret is
  wrong.

### Step 3 — remember: client secret ≠ user password

The portal authenticates as a UAA **client**, not a user. The `cf login`
admin *password* will never work as `UAA_CLIENT_SECRET`, no matter how
privileged that user is. See "Client secret ≠ user password" in
[cf-uaa-setup.md](cf-uaa-setup.md) for the two curls that tell them apart.

### Step 4 — create the client on this foundation

```bash
uaac target "$UAA_URL"
uaac token client get admin -s <admin-CLIENT-secret>
uaac client add cf-mgmt-portal \
  --authorized_grant_types client_credentials \
  --authorities cloud_controller.admin_read_only \
  -s <new-secret>
```

Then `cf set-env cf-mgmt-portal UAA_CLIENT_SECRET <new-secret>` and restage.

### Smoke-testing with the built-in `admin` client

The only pre-existing credential usable as a drop-in test is the UAA `admin`
**client** (client ID literally `admin`) — it has the `client_credentials`
grant and full read authority. Where the secret lives:

- **TAS / Ops Manager**: TAS tile → Credentials → **UAA → Admin Client
  Credentials** (distinct from "Admin Credentials", which is the user).
- **OSS CF / BOSH**: CredHub / vars store, typically
  `uaa_admin_client_secret`.

Test with the Step 2 curl using `client_id=admin`. Two cautions:

1. The admin client has **full write authority** — using it violates the
   portal's read-only-authz-client design. Smoke test only; swap in the
   least-privilege `cf-mgmt-portal` client before leaving it running.
2. Don't leave the admin secret in `cf env` or the manifest afterwards.

---

## 4. Wrong user identity: portal shows `name.lastname` instead of `F7PAYU0`

> **Resolved by design:** this issue is what motivated the switch to UAA
> login. With UAA as IdP the `user_name` claim from `/userinfo` *is* the
> sAMAccountName, so the mismatch can't occur. Kept for history / in case a
> GitLab-login deployment resurfaces.

The portal takes `username` verbatim from GitLab's `GET /api/v4/user`
(`internal/auth`) and assumes it equals the LDAP `sAMAccountName`. That holds
only on GitLab instances whose usernames *are* the LDAP IDs. If the instance
provisions `firstname.lastname` usernames, the assumption breaks — and it's
not cosmetic: this string is what gets checked against CF
(`/v3/users?usernames=…&origins=ldap`) and written into `spaceConfig.yml`, so
authz fails and MRs would carry the wrong identity.

### Diagnose — inspect what GitLab exposes for your account

```bash
curl -s -H "PRIVATE-TOKEN: <your-pat>" "$GITLAB_URL/api/v4/user" \
  | python3 -m json.tool
```

Look at the `identities` array. For LDAP-synced accounts it carries the LDAP
linkage, e.g.:

```json
"identities": [
  {"provider": "ldapmain", "extern_uid": "cn=f7payu0,ou=users,dc=…"}
]
```

If the `extern_uid` DN's CN is the sAMAccountName, the fix is a code change in
`internal/auth.Exchange`: decode `identities`, parse the LDAP ID out of the
DN, and use that as `User.Username` instead of the display username. If the
DN doesn't carry the ID (e.g. `CN=Lastname\, Firstname` style), fall back to
an LDAP lookup by email or a UAA `/Users?filter=email eq …` query.

---

## Quick reference: which error means what

| Symptom | Layer | Usual cause |
|---|---|---|
| "unknown client / no client authentication" at login | GitLab OAuth | OAuth app not registered on this instance / stale ID / secret regenerated |
| `invalid_grant` from a curl with a bogus code | GitLab OAuth | **Credentials are fine** — mismatch is in the running app's env |
| `404 Project Not Found` | GitLab API | `CONFIG_REPO_PROJECT` truncated to the group path, or token lacks project membership (GitLab 404s instead of 403) |
| "invalid client credentials" on org lookup | UAA | UAA client doesn't exist on this foundation, or user password used as client secret |
| User ID shows `name.lastname` | Portal identity | GitLab username ≠ sAMAccountName on this instance; read the LDAP `identities` instead |
