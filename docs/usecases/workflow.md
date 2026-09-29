The way this *should* work is that there will be a set of bare-bones VM templates that can be used to then expand upon during the deployments. The template VMs are intentionally kept minimal and up to date with security patches.

For example, we would have a few VMs:

1. Fedora Workstation 43
2. Fedora Workstation 44
3. Fedora Server 43
4. Fedora Server 44

There would also be `Fedora Workstation Stable` and `Fedora Server Stable` templates that would always point to the latest stable release of Fedora Workstation and Fedora Server respectively.

The names above would be the pretty names, and then they would correspond to a string ID `og-template-fedora-workstation-43`, `og-template-fedora-workstation-44`, `og-template-fedora-server-43`, `og-template-fedora-server-44`, `og-template-fedora-workstation-stable`, and `og-template-fedora-server-stable`.

Those string IDs would be hard-coded to map to the actual VM IDs on Proxmox. Note that we're using VMs and not actual templates because we will still apply security patches/updates to the templates on some schedule. This can be automated eventually, but for now we don't care.

The configuration process of these VMs would be intentionally basic and simple:

1. Create a default username/password (e.g. `organesson-creation:organesson-creation`)
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

Some other important considerations:

1. The concept of requesting a "public network range" for your deployment. This would be a range of IP v4 or v6 addresses that are sequential and fit within the smallest CIDR block possible to give the deployment at least enough addresses it needs. Organesson would own a large subnet (v4 / 12, some subset of a v6 / 64) and would be responsible for allocating smaller subnets to each deployment. This would allow for better network isolation and management of IP addresses. Deployments would still be asked to create their own network resources when able, but this allows some stuff to be "public" on the network Organesson manages. If an address is requested for a VM, the interface which is allowed to use it would then need to have a Proxmox firewall configuration to lock down the interface to only really work with the requested address. This would be a security measure to prevent accidental or malicious use of the public IP addresses.
2. The concept of "looping/functions". We could have some deployment of some VMs that would be good, but maybe this is some class activity or competition where the same VM needs to be deployed multiple times in a predictable manner. Let's say each student gets a firewall with a v4 address which is consumed from a requested range, and then there's a LAN network on the other side of the firewall which connects to two linux VMs. The only stuff that's different per-deployment would be the "wan address" of the firewall. Since each lan would be identical and also unique to that student's deployment.
3. The concept of sharing files between organesson and VMs. In the deployment configuration, there may also be an artifacts directory for any artifacts that can be temporarily mounted to the VMs. My concern is that the files must live on organesson, and should not be copied to the Proxmox host or the VMs. We need a way to create a virtual ISO that doesn't live on Proxmox, but can be mounted to the VMs.
4. The concept of logical resource groups. If we are deploying a bunch of copies of the same infrastructure like mentioned above, we may want to group them together. A logical group is only used for organizational purposes and is different from a user group. These logical groups can be children of other logical groups. The resources in a logical group can be managed together, but they are still individual resources and can be managed individually as well. This allows for better organization and management of the resources in a deployment.

This should all be validated and not blindly trusted to be safe. We shouldn't allow the deployment config to really touch proxmox directly since that's scary. In other words, this deployment can't configure Proxmox or touch anything that is not part of the deployment. The deployment should only be able to configure the VMs and networks that it owns. This is a security measure to prevent accidental or malicious changes to the Proxmox environment. The Proxmox cluster/environment can still independently be used for other things. 

The package format is to be determined, but ideally 