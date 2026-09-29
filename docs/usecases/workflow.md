The way this *should* work is that there will be a set of bare-bones VM templates that can be used to then expand upon during the deployments. The template VMs are intentionally kept minimal and up to date with security patches.

For example, we would have a few VMs:

1. Fedora Workstation 43
2. Fedora Workstation 44
3. Fedora Server 43
4. Fedora Server 44

There would also be `Fedora Workstation Latest` and `Fedora Server Latest` templates that would always point to the latest release of Fedora Workstation and Fedora Server respectively.

The names above would be the pretty names, and then they would correspond to a string ID `og-template-fedora-workstation-43`, `og-template-fedora-workstation-44`, `og-template-fedora-server-43`, `og-template-fedora-server-44`, `og-template-fedora-workstation-latest`, and `og-template-fedora-server-latest`.

Those string IDs would be hard-coded to map to the actual VM IDs on Proxmox. Note that we're using VMs and not actual templates because we will still apply security patches/updates to the templates on some schedule. This can be automated eventually, but for now we don't care.

The configuration process of these VMs would be intentionally basic and simple:

1. Create a template management administrative account with username/password (e.g. `organesson-creation:organesson-creation`)
2. Run system updates
3. Ensure that `qemu-guest-agent` is installed and running and test an execution through the agent through the organession API.

Then all we have to do is stop the VM and only lock provisioning from it when we need to run an update. The VM will be started, updated, and then stopped again. This will ensure that the templates are always up to date with the latest security patches.

To create a VM from the template, we would:

1. Use the string ID to look up the corresponding Proxmox VM ID.
2. Clone the VM from the template.
3. Remove the existing NIC. Reconfigure CPU, RAM, Disk(s), NIC(s) in Proxmox (through API).
4. Start the VM and wait for it to boot.
5. Use the qemu execution to resize the file system with the new disk size (and potentially other storage options).
6. Use the qemu execution to change the hostname and any other necessary identity configurations.
7. Use the qemu execution to delete the old user/group and then create any user configurations that are needed for the new VM.
8. Stop the VM and then take a snapshot of it. This snapshot will be used to roll back to a clean state if needed.
9. Start the VM and run any additional configuration scripts or commands that are needed for the specific use case of the VM.
10. If anything fails, we should use the failure policy defined in the whole setup to determine what to do next. Two main options would be: fail fast and stop all the provisioning for this task, or continue and log the error for later review.
11. Once the VM is fully configured and tested we can stop it again and take a new snapshot. This can be used for rollbacks in the future if needed.

It's worth noting that user credentials should be specified safely. You can specify the user, if it's an admin user, and then the password. The password should be hashed (maybe by `openssl passwd -6` or similar, it's worth considering if this is a linux/windows type VM and then what methods of storing the password are safe). Optionally, SSH keys can be specified for the user.

## Networking

An environment may have one or more administrator-configured environment networks. The `cyber.lab` network (the network I, the developer will be using whilst developing) is the default environment network: it is shared infrastructure that Proxmox, Organesson, and other systems can use. An environment network defines its address pools, gateways, DNS configuration, and network policy.

A deployment can request a named pool containing a number of individual IPv4 or IPv6 addresses from an environment network. A resource interface consumes one or more addresses from exactly one such pool; all addresses requested by that interface must therefore come from the same environment-network source. Organesson validates that the pool belongs to the deployment and attachment's environment network, and that aggregate interface requests do not exceed the approved pool capacity. This is an address allocation, not a request for a new subnet, CIDR block, routing authority, or permission to change the environment network. Allocated addresses should be protected with the appropriate Proxmox firewall or anti-spoofing configuration so another deployment resource cannot claim them.

Deployments can also create virtual network resources. A virtual network is owned by its deployment and is separate from an environment network. There are two initial capabilities:

1. An unmanaged Layer 2 virtual network provides isolated connectivity only. Organesson does not allocate a subnet, provide DHCP, DNS, a gateway, or routing. This is appropriate when the deployment provides its own router or firewall, or when a small number of devices use static addresses.
2. A managed virtual network provides Organesson-allocated IPv4 and/or IPv6 subnets and may provide a gateway, DHCP, DNS, and routing according to environment policy. The deployment requests the required capacity and capabilities; Organesson selects the actual allocation.

Every virtual network must have an explicit egress policy. It may be isolated, permitted to reach a selected environment network such as `cyber.lab`, or permitted to use a platform-managed route to another approved network. Being able to reach `cyber.lab` and being Internet-capable are separate capabilities and must not be implied by one another.

The concept of "looping/functions" is important. A class activity or competition may need the same topology many times in a predictable manner. For example, each student can receive a firewall with an individually reserved address from an environment network, plus a unique LAN behind that firewall with two Linux VMs. The deployment system should expand this safely while maintaining separate ownership, names, resources, allocations, and audit records for each copy.

## Artifact delivery and first-time setup

An artifact is a file made available to a deployment resource during provisioning, such as an unattended-install configuration, a setup script, a configuration file, a package, or a certificate. Artifact content is uploaded to Organesson with its expected identity and integrity information. A deployment can explicitly request that an artifact run inside one of its own guests during a supported setup phase, but an artifact must never execute on the Organesson or PVE host, select arbitrary PVE operations, or affect another deployment's resources.

Organesson must support an offline artifact-delivery path for guests that do not have an Internet-enabled interface. For a VM, Organesson generates a temporary, deployment- and resource-specific read-only ISO from the artifacts required for that provisioning operation. The ISO is attached to the VM as virtual media. The PVE host must hold the ISO only while it is attached, because QEMU must be able to read it; it is not a permanent PVE template or general-purpose artifact store.

The temporary ISO supports two capabilities:

1. A guest can execute a setup script directly from the mounted read-only media. This is suitable for a setup script too large for guest-agent execution and leaves no copy of the script on the guest after the media is detached.
2. A guest can copy selected artifacts from the media to its writable filesystem when persistence is intentional, for example to install a managed file under `/opt`.

First-time setup has two paths. A clone from a validated template starts with QEMU guest agent available, so Organesson can perform the required initial configuration through the guest agent and attach temporary media when a larger artifact is needed. An operating-system installation from an ISO starts without a guest agent; Organesson attaches a separate generated configuration ISO for the unattended installer and first-boot setup. That setup installs and starts the guest agent before the VM is considered ready for normal lifecycle operations.

Once the requested artifact work is confirmed, Organesson detaches the temporary media, deletes the temporary ISO from its dedicated PVE storage, and records creation, attachment, guest confirmation, detachment, and deletion in the deployment audit trail. Failure handling must not delete media before the resource has either completed setup or been marked failed for investigation.

## Logical resource groups

If a deployment contains copies of the same infrastructure, it may group them together. A logical resource group is only used for organizational purposes and is different from a user group. Logical groups can be children of other logical groups. Resources in a logical group can be managed together, but they are still individual resources and can be managed individually as well.

---

This should all be validated and not blindly trusted to be safe. We shouldn't allow the deployment config to really touch proxmox directly since that's scary. In other words, this deployment can't configure Proxmox or touch anything that is not part of the deployment. The deployment should only be able to configure the VMs and networks that it owns. This is a security measure to prevent accidental or malicious changes to the Proxmox environment. The Proxmox cluster/environment can still independently be used for other things.

Any VM created/managed by Organesson should be monitored for changes that could be made from the Proxmox side. If a VM is changed outside of Organesson, the change should be reflected in Organesson. If a VM is deleted outside of Organesson, the VM should be marked as deleted in Organesson. If a VM is created outside of Organesson, the VM should be completely ignored.

The package format is to be determined, but ideally the final artifact is a zip file.
