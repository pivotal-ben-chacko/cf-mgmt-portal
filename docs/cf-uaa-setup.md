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
only read what `internal/cfapi` needs (`cloud_controller.admin_read_only` — read
every org, user, and role, with no write capability).

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
  --authorities cloud_controller.admin_read_only \
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
    "authorities": ["cloud_controller.admin_read_only"]
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

## CF-side prerequisites for an action to pass

Even with valid creds, the authz check (and the action) only succeeds when:

1. **The target org is a real CF org.** Org names are **case-sensitive**
   (`system`, not `System`). Rename the seeded `demo-org` in the config repo to
   a real org. The org must also exist in the repo tree
   (`fog/<org>/spaces.yml`, `fog/<org>/<space>/spaceConfig.yml`).
2. **The logged-in user is OrgManager on that org.** The check is specifically
   for `organization_manager`; auditors/developers are rejected.
3. **The user exists in CF with origin `ldap`.** The lookup is
   `usernames=<id>&origins=ldap`; a user only present in GitLab (not synced into
   CF/UAA as an LDAP user) won't be found, and the check fails as "not a
   manager."

## Just testing the login flow?

Login never calls UAA/CF — only the *actions* do. To boot the app and watch the
OAuth flow without a working service account, any non-empty `UAA_CLIENT_*`
placeholder is enough for startup (the app requires the vars to be set, but
won't use them until you submit an action).
