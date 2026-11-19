# Resource reference

All resources are namespaced, in API group `harbor.graphzc.io/v1alpha1`.
Every resource except `HarborConnection` names a `HarborConnection` in the same
namespace with `spec.harborConnectionRef.name`. Secret references always point
to Secrets in the same namespace.

## Common behavior

- **Ready condition.** Each resource reports `status.conditions[type=Ready]`.
  When it is `False`, the reason says why:
  - `ConnectionError`: a referenced object is missing (connection or Secret).
    Retried every 30 seconds.
  - `HarborError`: Harbor rejected a request. The message holds Harbor's error.
  - `DeleteError`: Harbor refused a deletion (projects only).
- **Resync.** Resources are reconciled every 10 minutes, so changes made directly
  in Harbor are reverted. They are also reconciled when the spec, the
  `HarborConnection`, or a referenced Secret changes.
- **Adoption.** An existing Harbor object with the same name is taken over.
- **Deletion.** Except for `Configuration`, `deletionPolicy: Delete` (default) removes the Harbor object;
  `Orphan` leaves it. If the `HarborConnection` or its Secret no longer exists,
  the resource is released without cleanup.
- **Generated credentials.** When `User` or `RobotAccount` has no Secret reference,
  the operator writes the credential to `generatedSecret.name` (default
  `<metadata.name>-harbor-credentials`) with keys `username` and `password`.
  The Secret is owned by the resource and deleted with it. The operator
  refuses to overwrite a Secret it does not own.
- **Credential format.** Harbor requires passwords and robot secrets to be 8–128
  characters with at least one upper-case letter, one lower-case letter and one digit.

## HarborConnection

| Field | Required | Description |
|-------|----------|-------------|
| `url` | yes | Harbor URL, `http://` or `https://`. |
| `username` | | Admin username. Default `admin`. |
| `passwordSecretRef.name` / `.key` | yes | Secret holding the admin password. Key defaults to `password`. |
| `insecureSkipVerify` | | Skip TLS verification. |
| `caSecretRef.name` / `.key` | | Secret holding a PEM CA bundle. |

Status: `harborVersion`, `authMode`, `lastCheckedTime`. Ready requires the
credentials to belong to a Harbor system administrator.

## User

| Field | Required | Description |
|-------|----------|-------------|
| `username` | yes | Harbor username. Immutable. |
| `email` | yes | Email address. |
| `realName` | | Display name. Defaults to `username`. |
| `comment` | | Comment. |
| `sysAdmin` | | Grant system administrator. |
| `passwordSecretRef` | | Secret key with the password. Omit to generate one. |
| `generatedSecret.name` | | Name of the generated Secret. |
| `deletionPolicy` | | `Delete` (default) or `Orphan`. |

Status: `userID`, `secretName`, `passwordHash` (bcrypt). Changing the referenced
Secret changes the password in Harbor. The `HarborConnection` user cannot be
managed as a `User`.

```yaml
apiVersion: harbor.graphzc.io/v1alpha1
kind: User
metadata:
  name: alice
spec:
  harborConnectionRef: { name: harbor }
  username: alice
  email: alice@example.com
```

## Group

| Field | Required | Description |
|-------|----------|-------------|
| `groupName` | yes | Group name. |
| `groupType` | | `HTTP` (default), `LDAP` or `OIDC`. Immutable. |
| `ldapGroupDN` | for LDAP | Distinguished name of the LDAP group. |
| `deletionPolicy` | | `Delete` (default) or `Orphan`. |

Status: `groupID`.

## Project

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Project name (lower case, digits, `.`, `_`, `-`). Immutable. |
| `public` | | Public project. Default `false`. |
| `storageQuota` | | Storage limit, e.g. `50Gi`. Unlimited when omitted. |
| `policies.autoScan` | | Scan images on push. |
| `policies.autoSBOMGeneration` | | Generate SBOMs on push. |
| `policies.preventVulnerable` | | Block pulls of images at or above `severity`. |
| `policies.severity` | | `none`, `low`, `medium`, `high` or `critical`. |
| `policies.contentTrust` | | Allow only Notary-signed images. |
| `policies.contentTrustCosign` | | Allow only cosign-signed images. |
| `policies.reuseSysCVEAllowlist` | | Use the system CVE allowlist. |
| `policies.cveAllowlist.items` | | CVE IDs allowed in this project. |
| `policies.cveAllowlist.expiresAt` | | When the allowlist expires. |
| `members[].kind` | | `User` (default) or `Group`. |
| `members[].name` | yes | Username or group name. |
| `members[].role` | yes | `ProjectAdmin`, `Maintainer`, `Developer`, `Guest` or `LimitedGuest`. |
| `deletionPolicy` | | `Delete` (default) or `Orphan`. |

Status: `projectID`, `storageUsed`.

- Unset `policies` fields are left as they are in Harbor.
- A non-empty `members` list is authoritative. Members not listed are removed,
  except the `HarborConnection` user, whom Harbor adds as the project creator.
  An empty list leaves membership unmanaged.
- Members must already exist in Harbor. They can be managed by `User` and
  `Group` resources; until they exist, the project reports a `HarborError`.
- Harbor refuses to delete a project that still has repositories. Delete
  them or set `deletionPolicy: Orphan`.

## RobotAccount

| Field | Required | Description |
|-------|----------|-------------|
| `name` | yes | Robot name without prefix. Immutable. |
| `level` | | `system` (default) or `project`. Immutable. |
| `project` | for project level | Project the robot belongs to. |
| `description` | | Description. |
| `duration` | | Lifetime in days; `-1` (default) never expires. |
| `disabled` | | Disable the robot. |
| `permissions[].kind` | | `project` (default) or `system`. |
| `permissions[].namespace` | | Project name, `*` for all projects, or `/` for system permissions. Ignored for project robots, which always use `project`. |
| `permissions[].access[]` | yes | `resource`/`action` pairs, e.g. `repository`/`pull`, with optional `effect` (`allow`/`deny`). |
| `secretRef` | | Secret key with the robot secret. Omit to use Harbor's generated secret. |
| `generatedSecret.name` | | Name of the generated Secret. |
| `deletionPolicy` | | `Delete` (default) or `Orphan`. |

Status: `robotID`, `fullName` (the login name, e.g. `robot$team-a+ci`),
`expiresAt`, `secretName`, `secretHash`.

- Changing the referenced Secret sets the new secret in Harbor.
- If the generated Secret is deleted, the operator recreates it. The value is
  either the current secret or a new one from Harbor.

Common access pairs: `repository` with `pull`, `push`, `delete` or `list`;
`artifact` with `read`, `list` or `delete`; `tag` with `create`, `delete` or
`list`; `scan` with `create` or `read`; `helm-chart` with `read`.

```yaml
apiVersion: harbor.graphzc.io/v1alpha1
kind: RobotAccount
metadata:
  name: puller
spec:
  harborConnectionRef: { name: harbor }
  name: puller
  permissions:
    - namespace: "*"
      access:
        - { resource: repository, action: pull }
```

## Configuration

Manages Harbor system settings through `/api/v2.0/configurations`.

| Field | Required | Description |
|-------|----------|-------------|
| `settings` | | Map of camelCase configuration key to value, e.g. `authMode`, `projectCreationRestriction`, `robotTokenDuration`, `oidcEndpoint`, `ldapUrl`. |
| `secretSettings` | | Map of camelCase configuration key to a Secret key, for credentials such as `oidcClientSecret` or `ldapSearchPassword`. A key cannot be in both maps. |

Status: `secretSettingsHash` (bcrypt).

### Keys

Keys are the camelCase form of the snake_case keys returned by
`GET /api/v2.0/configurations`. The operator converts them back before calling
Harbor. Value types are Harbor's, and values are sent unchanged, so
`authMode: oidc_auth` keeps its snake_case value.

| Harbor key | Key in `settings` / `secretSettings` |
|------------|--------------------------------------|
| `auth_mode` | `authMode` |
| `project_creation_restriction` | `projectCreationRestriction` |
| `self_registration` | `selfRegistration` |
| `read_only` | `readOnly` |
| `token_expiration` | `tokenExpiration` |
| `robot_token_duration` | `robotTokenDuration` |
| `robot_name_prefix` | `robotNamePrefix` |
| `oidc_endpoint` | `oidcEndpoint` |
| `oidc_client_id` | `oidcClientId` (or `oidcClientID`) |
| `oidc_client_secret` | `oidcClientSecret` |
| `ldap_url` | `ldapUrl` (or `ldapURL`) |
| `ldap_search_password` | `ldapSearchPassword` |

- Keys must match `^[a-z][a-zA-Z0-9]*$`. A snake_case key makes the resource
  report `Ready=False` with the camelCase form to use. In `secretSettings` it is
  rejected at admission.
- A run of capitals counts as one word, so `ldapURL` and `ldapUrl` both mean
  `ldap_url`. Setting both, or setting a key in both maps, is reported as an
  error.

### Behavior

- Only listed keys are managed; every other setting is left as it is in Harbor.
  Listed keys that drift are set back on the next resync.
- Only keys whose value differs are sent to Harbor. Scalars are compared by
  their string form, so `"90"` and `90` are equal.
- Harbor never returns credentials, so `secretSettings` are applied when the
  referenced Secret values change, not when they drift in Harbor.
- Unknown keys, keys Harbor reports as not editable (for example, set through
  environment variables), and values Harbor rejects make the resource report
  `Ready=False`. Harbor's messages name the snake_case key. Harbor only allows
  changing `authMode` while no users other than `admin` exist.
- There is no `deletionPolicy`: Harbor has a single configuration, so deleting
  the resource leaves the settings in place.
- Use one `Configuration` per Harbor instance, or give each one distinct keys.
  Two resources that set the same key keep overwriting each other.

```yaml
apiVersion: harbor.graphzc.io/v1alpha1
kind: Configuration
metadata:
  name: harbor
spec:
  harborConnectionRef: { name: harbor }
  settings:
    authMode: oidc_auth
    oidcName: keycloak
    oidcEndpoint: https://keycloak.example.com/realms/harbor
    oidcClientId: harbor
    oidcScope: openid,profile,email,offline_access
    projectCreationRestriction: adminonly
  secretSettings:
    oidcClientSecret: { name: harbor-oidc, key: clientSecret }
```
