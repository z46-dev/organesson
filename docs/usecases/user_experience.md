This builds off of workflow.md and defines how the web ui would work from multiple perspectives.

There would be one unified dashboard which has some components which are available on certain things based on the user access level in that thing.

So there are the following resources which have actions that may be done to them:

1. Virtual Machine
    1. Power Control (Start, Stop, Restart, Suspend, Resume)
    2. Console Control (Open the console and send inputs)
    3. Snapshot Control (Create, Restore, Delete) (This would be within a storage quota, aka 32GB storage quota for snapshots, you would need to delete one to make room if you reach it)
    4. Configuration Control (Change CPU, RAM, Disk, NICs, etc.)
    5. Administrative Control (Delete, Clone, Move, etc.)
2. Container
    1. Power Control (Start, Stop, Restart, Suspend, Resume)
    2. Console Control (Open the console and send inputs)
    3. Snapshot Control (Create, Restore, Delete) (This would be within a storage quota, aka 32GB storage quota for snapshots, you would need to delete one to make room if you reach it)
    4. Configuration Control (Change CPU, RAM, Disk, NICs, etc.)
    5. Administrative Control (Delete, Clone, Move, etc.)
3. Network
    1. Create/Delete/Modify Networks
    2. Create/Delete/Modify Subnets
    3. Create/Delete/Modify Firewall Rules

Everything in organesson is organized (haha) into a "deployment". There are two types of deployments: automated and manual.

Automated deployments are much simpler. Actions that can be performed on those deployments are limited to view/audit, power control, snapshot management, and potential redeployment of certain individual resources or groups of resources. The deployment itself is not editable, and the resources within it are not editable. These are created via a configuration package.

A manual deployment is much more flexible. Resources inside a manual deployment can be created, modified, and deleted at will by those with proper permissions to do so. A configuration package may be used to kickstart the creation of a manual deployment, but it's not necessary. Configuration packages can also be ran on existing manual deployments to create more resources.

The access control of the resources within a deployment is defined by the deployment configuration package. The package defines the roles and responsibilities of each user in relation to the resources in the deployment. The package also defines what actions can be performed on each resource by each user. Access control roles can be bound to a user or a group. A group can have multiple users, and a user can be in multiple groups.

Another important permission aspect of this is that there are permissions that relate to the deployment itself. These permissions are more administrative and can be used to grant other permissions in some cases:

- Deployment Admin: Can manage the deployment itself, including adding/removing users, changing roles/responsibilities of users in relation to the resources in the deployment, and changing the configuration of the deployment itself. This is a very powerful permission and should be granted sparingly.
- Deployment Manager: Can manage the users and groups in the deployment, including adding/removing users, changing roles/responsibilities of users in relation to the resources in the deployment, and changing the configuration of the deployment itself. It would not allow the user to grant/revoke the Deployment Admin permission to themselves or others.

---

Scenario:

- I am the administrator.
- Alice is a user.
- Bob is a user.
- Charlie is a user.
- Dave is a user.

Alice comes and speaks to me and says "Hey I'm teaching a class. My students are Charlie and Dave, and Bob is my TA. I've made a package that will create a deployment for my class. Can you deploy it for me? I don't need any major changes after it's been deployed, just basic administration stuff."

Alice's package creates for each student:
- A LAN network device (with no subnet or connected devices)
- Two Linux VMs connected to the LAN
- One of the VMs will also have a NIC on the `cyber.lab` network, and will get its IP address from a request from a pool for the entire deployment.

He also discusses that:
- Each student can view, power control, and console control their own VMs.
- The TA, Bob, can view, power control, console control, and snapshot control all of the VMs in the deployment.
- Alice can view, power control, console control, and snapshot control all of the VMs in the deployment.

So I create a deployment for Alice. The package details the roles/responsibilities of Alice, Bob, Charlie, and Dave in relation to the resources in the deployment. By deploying the package, I have created a deployment that has the following resources:
- 2 Firewalls (one for Charlie, one for Dave)
- 4 Linux VMs (two for Charlie, two for Dave)

Alice also detailed herself as an administrator of the deployment. Thus, she can manage the access control of the deployment. However, because I am an administrator of the platform, I can also manage the access control of the deployment. I can add/remove users, change roles, and change responsibilities of users in relation to the resources in the deployment. Alice cannot change my absolute administrative status of the deployment, but I can change hers. I can also change the roles/responsibilities of Bob, Charlie, and Dave in relation to the resources in the deployment.

---

The UI shall have a unified dashboard that will display the deployments and their resources that the user can see. We will take a Proxmox-style approach. There shall be a left-hand sidebar which will have a tree-style view of the deployments and their resources. The "top-level" of each tree would be the name of the deployment. The next level would have tabs for "Resources" "Users" "Groups" "Permissions". These would each have children respectively:
- Resources: Only resources the user has audit/view perms for. If a resource is part of a group, it will be displayed under the group. If a resource is not part of a group, it will be displayed after all groups the user can see. If the resource belongs to a group, and the group it belongs to is part of a group, the group will instead be displayed under the parent group.
    Clicking on a group will change the main section to display the resources in that group and any child groups of that group.
    Clicking on a resource will change the main section to display the details of that resource and any actions that can be performed on it along with details. Actions displayed will be based on the user's permissions for that resource.
- Users: If the user is a "Deployment Admin" or "Deployment Manager", they will be able to see all users in the deployment. If the user is a "Deployment Auditor", they will only be able to see themselves. Clicking on a user will change the main section to display the details of that user and any actions that can be performed on them along with details. Actions displayed will be based on the user's permissions for that user.
- Groups: If the user is a "Deployment Admin" or "Deployment Manager", they will be able to see all groups in the deployment. If the user is a "Deployment Auditor", they will only be able to see groups they belong to. Clicking on a group will change the main section to display the details of that group and any actions that can be performed on it along with details. Actions displayed will be based on the user's permissions for that group. If you are part of a group you can see who is in the group.
- Permissions: If the user is a "Deployment Admin" or "Deployment Manager", they will be able to see all permission mappings in the deployment. Permissions mappings are the relationships between a single user or group and a single permission on a single target. If the user is a "Deployment Auditor", they will only be able to see permission mappings that involve themselves. Clicking on a permission mapping will change the main section to display the details of that permission mapping and any actions that can be performed on it along with details. Actions displayed will be based on the user's permissions for that permission mapping.

It's worth noting that permissions mappings could be something like `{user: Alice, permission: VM.PowerControl, target: logical resource group 1234}`. This would mean that any resource that is a direct or indirect child of logical resource group 1234, Alice would have the ability to power control that resource.

The UI shall have an administrator page separate from the dashboard which will allow the administrators to manage the configuration of the application and how it relates to the Proxmox cluster, and how it sources data from a LDAP/FreeIPA domain. Quotas and other important things will live here as well. This page will be accessible to those with the "Administrator" role in the application.