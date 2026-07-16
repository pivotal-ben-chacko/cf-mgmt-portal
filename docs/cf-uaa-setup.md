# CF / UAA setup for cf-mgmt-portal

How to produce the `CF_API_URL`, `UAA_URL`, `UAA_CLIENT_ID`, and
`UAA_CLIENT_SECRET` env vars, and the CF-side prerequisites an action needs to
succeed.

## What this is for

When a user submits an action (add user to a space role, create a space), the
**first thing the portal does** is verify the *logged-in user* holds the right
role on the target CF **org** — e.g. "is `F920U2K` an OrgManager on `system`?"
(`internal/cfapi`). It answers that by calling the CF API v3:

```
GET /v3/organizations?names=<org>
GET /v3/users?usernames=<ldap-id>&origins=ldap
GET /v3/roles?organization_guids=…&user_guids=…&types=organization_manager
```

To make those calls it needs a token, which it gets from UAA using the
**client_credentials** grant. So `UAA_CLIENT_*` is a **service account**,
separate from the GitLab login. The portal only ever **reads** CF — it never
writes (all CF changes flow through the cf-mgmt pipeline after the MR merges),
so the service account should be **read-only**.

| Var | Example | Notes |
|---|---|---|
| `CF_API_URL` | `https://api.system.skynetsystems.io` | CF API root |
| `UAA_URL` | `https://uaa.system.skynetsystems.io` | UAA root |
| `UAA_CLIENT_ID` | `cf-mgmt-portal` | a UAA **client**, not a user |
| `UAA_CLIENT_SECRET` | `…` | the **client secret**, not a user password |

## ⚠️ Client secret ≠ user password

The portal logs in to UAA **as a client** (`client_credentials`), not as a
user. A very common mistake is to use the **admin _user_ password** (the
`cf login` credential) as `UAA_CLIENT_SECRET`. That fails with:

```
oauth2: "invalid_client" "Bad credentials"
```

The admin **user password** and the admin **client secret** are different
credentials that happen to share the name "admin". You can tell them apart:

```bash
UAA=https://uaa.system.skynetsystems.io
# CLIENT secret -> should return an access_token:
curl -sk -X POST "$UAA/oauth/token" -d grant_type=client_credentials \
  -d client_id=admin --data-urlencode "client_secret=<value>"
# USER password -> works only via the password grant, proving it's NOT a client secret:
curl -sk -X POST "$UAA/oauth/token" -d grant_type=password -d client_id=cf -d client_secret= \
  -d username=admin --data-urlencode "password=<value>"
```

## Create the UAA client

Rather than reuse the `admin` client, create a least-privilege client that can
only read what `internal/cfapi` needs:

- `cloud_controller.admin_read_only` — read every org, user, and role, with no
  write capability.
- `scim.read` — read UAA users and their group memberships; only needed when
  `PORTAL_ADMIN_GROUPS` is set (see [Admin groups](#admin-groups-portal_admin_groups)).

> **`--authorities` vs `--scope`.** For a `client_credentials` client, set
> **`authorities`** — those are the permissions on the client's *own* token.
> `scope` only matters for clients that issue tokens *on behalf of a user*, so
> it's irrelevant here. Setting `scope` instead of `authorities` is a common
> reason the token comes back without CF read access.

### Step 1 — Get the UAA admin **client** secret (one-time bootstrap)

Creating a client requires admin authority. Pull the admin **client** secret
(not the user password — see the warning above) from your deployment:

```bash
credhub get -n /<bosh-director>/cf/uaa_admin_client_secret
# or, from a cf-deployment vars-store:
bosh int <vars-store>.yml --path /uaa_admin_client_secret
```

### Step 2 — Create the client

**Option A — with `uaac`** (`gem install cf-uaac` if you don't have it):

```bash
# Point uaac at your UAA and authenticate as the admin CLIENT:
uaac target https://uaa.system.skynetsystems.io --skip-ssl-validation
uaac token client get admin -s <UAA_ADMIN_CLIENT_SECRET>

# Create the read-only service account:
uaac client add cf-mgmt-portal \
  --name "cf-mgmt-portal authz service account" \
  --authorized_grant_types client_credentials \
  --authorities cloud_controller.admin_read_only,scim.read \
  --secret <CHOOSE_A_STRONG_SECRET>

# Confirm it exists (secret is not shown back):
uaac client get cf-mgmt-portal
```

**Option B — with the REST API** (no `uaac` needed; same result):

```bash
UAA=https://uaa.system.skynetsystems.io
# Mint an admin token:
T=$(curl -sk -X POST "$UAA/oauth/token" -d grant_type=client_credentials \
     -d client_id=admin --data-urlencode "client_secret=<UAA_ADMIN_CLIENT_SECRET>" \
     | python3 -c "import sys,json;print(json.load(sys.stdin)['access_token'])")

# Create the client:
curl -sk -X POST "$UAA/oauth/clients" \
  -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{
    "client_id": "cf-mgmt-portal",
    "name": "cf-mgmt-portal authz service account",
    "secret": "<CHOOSE_A_STRONG_SECRET>",
    "authorized_grant_types": ["client_credentials"],
    "authorities": ["cloud_controller.admin_read_only", "scim.read"]
  }'
```

### Step 3 — Wire it into the manifest

```yaml
UAA_CLIENT_ID: cf-mgmt-portal
UAA_CLIENT_SECRET: <CHOOSE_A_STRONG_SECRET>
```

(For real deployments, prefer `cf set-env cf-mgmt-portal UAA_CLIENT_SECRET …`
over committing it.)

### Rotating the secret later

```bash
# uaac:
uaac client update cf-mgmt-portal --secret <NEW_SECRET>
# REST:
curl -sk -X PUT "$UAA/oauth/clients/cf-mgmt-portal/secret" \
  -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"secret":"<NEW_SECRET>"}'
```

Then update `UAA_CLIENT_SECRET` and `cf restart` the portal.

### Verify the client works

```bash
UAA=https://uaa.system.skynetsystems.io
CFAPI=https://api.system.skynetsystems.io
T=$(curl -sk -X POST "$UAA/oauth/token" -d grant_type=client_credentials \
     -d client_id=cf-mgmt-portal --data-urlencode "client_secret=<secret>" \
     | python3 -c "import sys,json;print(json.load(sys.stdin)['access_token'])")
curl -sk -H "Authorization: Bearer $T" "$CFAPI/v3/organizations?per_page=5" \
  | python3 -c "import sys,json;print([o['name'] for o in json.load(sys.stdin)['resources']])"
```

If that prints org names, the service account is good.

## Admin groups (`PORTAL_ADMIN_GROUPS`)

`PORTAL_ADMIN_GROUPS` is a comma-separated list of UAA group names whose
members bypass the OrgManager check on every action (same effect as listing a
username in `PORTAL_ADMIN_USERS`; the platform team's MR review remains the
write gate). Before each action the portal looks the logged-in user up via UAA
SCIM (`GET /Users?filter=userName eq "<user>"`) with the service-account
client and compares the user's `groups[].display` names against the list.

- **Platform ops teams are usually covered by `cloud_controller.admin`** —
  every CF admin is a member of that UAA group, so
  `PORTAL_ADMIN_GROUPS: cloud_controller.admin` grants the whole team access
  with no extra UAA setup.
- **LDAP/AD groups** only appear as UAA groups if your UAA's LDAP integration
  maps them (`ldap.groups` profile / external group mappings). Verify what UAA
  actually reports for a user before relying on an AD group name:

  ```bash
  T=$(curl -sk -X POST "$UAA/oauth/token" -d grant_type=client_credentials \
       -d client_id=cf-mgmt-portal --data-urlencode "client_secret=<secret>" \
       | python3 -c "import sys,json;print(json.load(sys.stdin)['access_token'])")
  curl -sk -G "$UAA/Users" -H "Authorization: Bearer $T" \
    --data-urlencode 'filter=userName eq "F920U2K"' \
    --data-urlencode 'attributes=userName,groups' \
    | python3 -m json.tool
  ```

- Group names must match the SCIM `display` value **exactly** (case included).
- If the membership lookup fails (most commonly the client is missing
  `scim.read` — add it with
  `uaac client update cf-mgmt-portal --authorities cloud_controller.admin_read_only,scim.read`
  and restart the portal), the step is recorded as an error on the result page
  and the action falls back to the normal OrgManager check, so non-admin users
  are unaffected.

## Create the login client (`UAA_LOGIN_CLIENT_*`)

Since the switch away from GitLab OAuth, **user sign-in also goes through
UAA**: the portal redirects the browser to UAA's login page
(authorization-code flow, `openid` scope), and the `user_name` claim it gets
back from `/userinfo` is the LDAP `sAMAccountName`. This needs a **second,
separate client** — a login client has no CF API authorities at all, while the
service account above must never hold a browser-facing grant.

```bash
# (uaac already targeted + admin token from Step 1 above)
uaac client add cf-mgmt-portal-login \
  --name "cf-mgmt-portal user login" \
  --authorized_grant_types authorization_code,refresh_token \
  --scope openid \
  --autoapprove openid \
  --redirect_uri "<PORTAL_URL>/auth/callback" \
  --secret <CHOOSE_A_STRONG_SECRET>
```

Notes:

- The `redirect_uri` must match `$PORTAL_URL/auth/callback` **exactly**,
  scheme included. Add multiple `--redirect_uri` values if you also test on
  localhost.
- `--autoapprove openid` skips UAA's consent screen so login feels seamless.
- This client uses **`scope`**, not `authorities` — the inverse of the
  service-account rule above, because its tokens act *on behalf of the user*.
- If the browser redirect to `/oauth/authorize` errors on your foundation,
  point `UAA_URL` at the `login.system.<domain>` host instead of
  `uaa.system.<domain>` — some deployments only serve the login UI there
  (token and `/userinfo` work on both).

Wire it in:

```yaml
UAA_LOGIN_CLIENT_ID: cf-mgmt-portal-login
UAA_LOGIN_CLIENT_SECRET: <CHOOSE_A_STRONG_SECRET>
```

## CF-side prerequisites for an action to pass

Even with valid creds, the authz check (and the action) only succeeds when:

1. **The target org is a real CF org.** Org names are **case-sensitive**
   (`system`, not `System`). Rename the seeded `demo-org` in the config repo to
   a real org. The org must also exist in the repo tree
   (`fog/<org>/spaces.yml`, `fog/<org>/<space>/spaceConfig.yml`).
2. **The logged-in user is OrgManager on that org.** The check is specifically
   for `organization_manager`; auditors/developers are rejected.
3. **The user exists in CF with origin `ldap`.** The lookup is
   `usernames=<id>&origins=ldap`; a user whose only UAA identity has a
   different origin (e.g. a local `uaa`-origin test account) won't be found,
   and the check fails as "not a manager."

## Provisioning a user so an action succeeds (test / non-LDAP-synced foundations)

If your foundation's UAA has no real LDAP integration (e.g. the local dev
stack), the authz lookup (`origins=ldap`) can't find anyone, so every action
fails. To make an action work end-to-end you must hand-create a CF `ldap`
user whose **username equals the username you sign in to the portal with**,
give it OrgManager on an org, and make sure that org/space exists in the
config repo.

> **Local-dev sign-in without LDAP.** UAA's login page can only authenticate
> identities it can verify — and a hand-created `ldap`-origin user has no
> password UAA can check without a real LDAP server behind it. The trick: the
> authz lookup matches on the **username string**, not the session's origin.
> So create **two** UAA users with the same `userName`: a normal `uaa`-origin
> user with a password (this one you log in as) and the shadow `ldap`-origin
> user below (this one satisfies the role check).

> **Write scope needed.** The portal's `cf-mgmt-portal` client is
> **read-only** (`cloud_controller.admin_read_only`) and cannot do any of the
> writes below. Use a token with `scim.write` (to create the user) **and**
> `cloud_controller.admin` (to assign the role). The admin **user** token has
> both — get it via the password grant on the `cf` client:
>
> ```bash
> UAA=https://uaa.system.skynetsystems.io
> AT=$(curl -sk -X POST "$UAA/oauth/token" -d grant_type=password \
>   -d client_id=cf -d client_secret= -d username=admin \
>   --data-urlencode "password=<ADMIN_USER_PASSWORD>" \
>   | python3 -c "import sys,json;print(json.load(sys.stdin)['access_token'])")
> ```

Set the targets (must match what you'll type in the portal — names are
**case-sensitive**, e.g. `system`, not `System`):

```bash
CFAPI=https://api.system.skynetsystems.io
LOGIN_USER=root          # the username you sign in to the portal as (UAA login)
ORG=system               # an existing CF org
SPACE=dev
```

### 1. Create the UAA `ldap` user

CAPI will **not** shadow-create an `ldap` user from a username (it returns
`No user exists with the username '…' and origin 'ldap'`), so create it in UAA
first via SCIM:

```bash
UG=$(curl -sk -X POST "$UAA/Users" -H "Authorization: Bearer $AT" \
  -H 'Content-Type: application/json' -d "{
    \"userName\":\"$LOGIN_USER\",\"origin\":\"ldap\",\"active\":true,\"verified\":true,
    \"emails\":[{\"value\":\"$LOGIN_USER@example.local\",\"primary\":true}],
    \"name\":{\"givenName\":\"$LOGIN_USER\",\"familyName\":\"$LOGIN_USER\"}}" \
  | python3 -c "import sys,json;print(json.load(sys.stdin).get('id',''))")
echo "uaa user guid: $UG"
```

(If it already exists, look it up:
`curl -sk -G "$UAA/Users" -H "Authorization: Bearer $AT" --data-urlencode 'filter=userName eq "root" and origin eq "ldap"'`.)

### 2. Assign OrgManager on the org (by GUID)

```bash
OG=$(curl -sk -H "Authorization: Bearer $AT" "$CFAPI/v3/organizations?names=$ORG" \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['resources'][0]['guid'])")

curl -sk -X POST "$CFAPI/v3/roles" -H "Authorization: Bearer $AT" \
  -H 'Content-Type: application/json' -d "{
    \"type\":\"organization_manager\",
    \"relationships\":{\"user\":{\"data\":{\"guid\":\"$UG\"}},
                       \"organization\":{\"data\":{\"guid\":\"$OG\"}}}}"
```

### 3. Seed the org/space in the config repo

The action reads `fog/<org>/<space>/spaceConfig.yml`. Commit it on the
`TARGET_BRANCH` (`development`) — see the seed example in
[gitlab-setup.md](gitlab-setup.md#2-config-repo-project--config_repo_project).
Minimum for `add user to role`: `fog/system/dev/spaceConfig.yml` (and
`fog/system/spaces.yml` for `create space`).

### 4. Verify with the portal's own (read-only) client

Confirm the exact lookups the portal runs now succeed:

```bash
NT=$(curl -sk -X POST "$UAA/oauth/token" -d grant_type=client_credentials \
  -d client_id=cf-mgmt-portal --data-urlencode "client_secret=<PORTAL_CLIENT_SECRET>" \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['access_token'])")
PUG=$(curl -sk -H "Authorization: Bearer $NT" "$CFAPI/v3/users?usernames=$LOGIN_USER&origins=ldap" \
  | python3 -c "import sys,json;print(json.load(sys.stdin)['resources'][0]['guid'])")
curl -sk -H "Authorization: Bearer $NT" \
  "$CFAPI/v3/roles?organization_guids=$OG&user_guids=$PUG&types=organization_manager" \
  | python3 -c "import sys,json;print('PASS' if json.load(sys.stdin)['resources'] else 'FAIL')"
```

`PASS` means the next portal action for that user on `$ORG/$SPACE` will clear
the authz check and open an MR.

> **Note on identity matching.** The authz check is on the **logged-in** user,
> not the *target* user of the action. The target user is only written into the
> YAML and is never validated, so it can be any value. Only the user you
> sign in as needs the CF `ldap` + OrgManager setup above.

## Just testing the login flow?

Login uses only the `UAA_LOGIN_CLIENT_*` client (authorize → token →
`/userinfo`); the `UAA_CLIENT_*` service account is untouched until you submit
an *action*. So to boot the app and watch the login flow, the login client
must exist, but any non-empty `UAA_CLIENT_*` placeholder is enough for
startup.
