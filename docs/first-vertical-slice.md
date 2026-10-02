# First backend vertical slice

The original local vertical slice exercises SQLite initialization, administrator activation, session authentication, ownership records, and access checks. Simulated VMs still update Organesson's catalog only; the opt-in Proxmox lifecycle path is documented below.

## Run it by hand

1. From the repository root, start `go run ./backend`. The first run creates `config.toml`, the SQLite database, and `administrator@organesson`. Copy the one-time setup token from the console. The token is printed only when it is created; if it expires or the process exits before you copy it, stop the server and run `go run ./backend bootstrap reset-administrator-password`, then restart it.
2. Request a CSRF token and retain the cookies:

   ```sh
   curl -sS -c /tmp/organesson-cookies.txt -b /tmp/organesson-cookies.txt \
       http://127.0.0.1:6800/api/v1/auth/csrf
   ```

   Send the returned `csrf_token` as `X-Csrf-Token` and keep using the cookie jar for every state-changing request.
3. Redeem the startup token with a password of at least 12 bytes:

   ```sh
   curl -i -c /tmp/organesson-cookies.txt -b /tmp/organesson-cookies.txt \
       -H 'Content-Type: application/json' \
       -H 'X-Csrf-Token: <csrf-token>' \
       --data '{"token":"<startup-token>","password":"<password-at-least-12-characters>"}' \
       http://127.0.0.1:6800/api/v1/auth/bootstrap/redeem
   ```

   The response should be `201 Created`. Request a fresh CSRF token after setup because login rotates the session identifier.
4. As the administrator, create a deployment with `POST /api/v1/deployments` and `{"name":"class-lab","description":"Local access-control smoke test"}`. Use `deployment.id` and `deployment.root_node_id` from the response to create a VM with `POST /api/v1/deployments/{id}/virtual-machines` and `{"parent_node_id":<root-node-id>,"name":"student-fedora"}`. The VM response includes its ID and ownership-node ID.
5. Create a logical group under the deployment root with `POST /api/v1/deployments/{id}/logical-groups` and `{"parent_node_id":<root-node-id>,"name":"student-team"}`. Create a local user with `POST /api/v1/admin/accounts/local` and `{"username":"student1","display_name":"Student One"}`. Save the returned account ID and setup token; this response is `no-store` and the token is only shown once. A platform admin can reissue a link with `POST /api/v1/admin/accounts/local/{account-id}/password-link` if needed. Redeem the token on a separate cookie jar using `POST /api/v1/auth/password/redeem` with `{"token":"<setup-token>","password":"<password-at-least-12-characters>"}`. This activates the account; then log in at `POST /api/v1/auth/login` with `{"username":"student1@organesson","password":"<password>"}`.
6. Create a deployment user group with `POST /api/v1/deployments/{id}/user-groups` and `{"name":"students"}`. Add the new account using `POST /api/v1/user-groups/{group-id}/members` and `{"account_id":<account-id>}`. Grant the group access at the logical group node with `POST /api/v1/ownership-nodes/{logical-group-node-id}/grants` and `{"subject_kind":1,"subject_id":<group-id>,"permission":"resource.view"}`. The student should now see the deployment in `GET /api/v1/deployments`; before the grant, the list should be empty. `subject_kind` is `0` for an account and `1` for a deployment user group. Try `vm.power` instead of `resource.view` to separately grant power control on a node.
7. As the student, use `POST /api/v1/virtual-machines/{vm-id}/power` with `{"action":"start"}` only after granting `vm.power_control` on the VM’s ownership node or an ancestor. It should return `power_state: "running"`; without that permission it returns `403`. Remove the student from the group with `DELETE /api/v1/user-groups/{group-id}/members/{account-id}` and verify the deployment is no longer listed. `GET /api/v1/auth/me` shows the current account.
8. `GET /api/v1/deployments` without a session cookie should return `401`. A state-changing request without a valid CSRF token should return `403`.

The deployment workflow now includes an opt-in Proxmox VM path. It clones only catalog sources explicitly marked provisioning-ready, enforces the validated platform pool/storage/capacity policy, stores the resulting PVE placement, and uses PVE for live status and power operations. Provider smoke defaults remain simulated; the focused real-cluster run is documented in [Proxmox VM lifecycle milestone](proxmox-vm-lifecycle.md).

## Test the source VM catalog UI

Run the frontend with `cd frontend && bun run dev` while the backend is running. Sign in as `administrator@organesson`; register source VMs such as VMID `156` for Fedora Workstation and VMID `157` for Fedora Server, then run preflight and explicitly confirm readiness checks. Configure and validate the allowed PVE resources in Administration → Proxmox resources. For the small moving live acceptance fixture, see [`examples/artifact_smoke`](../examples/artifact_smoke/README.md) and the [Proxmox lifecycle checklist](proxmox-vm-lifecycle.md).

To reset the test instance, stop the server and remove only the database file named by `database.file` in your local `config.toml`. Starting again creates a new database and a new one-time setup token.
