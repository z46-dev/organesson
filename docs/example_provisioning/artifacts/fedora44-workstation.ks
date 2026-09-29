# Render this through Organesson before publication. Do not publish a reusable token.
url --url="http://ARTIFACT_HOST/provisionings/PROVISIONING_ID/fedora44-workstation"
text
reboot
lang en_US.UTF-8
keyboard us
timezone UTC --utc
network --bootproto=dhcp --device=link --activate
rootpw --lock
user --name=organesson --groups=wheel --password=REPLACE_WITH_HASH --iscrypted
zerombr
clearpart --all --initlabel
autopart --type=lvm
services --enabled=qemu-guest-agent
%packages
@workstation-product-environment
qemu-guest-agent
curl
%end
%post --erroronfail
systemctl enable qemu-guest-agent
curl --fail --show-error --header "Authorization: Bearer RUNTIME_TOKEN" \
    http://ARTIFACT_HOST/provisionings/PROVISIONING_ID/bootstrap-fedora.sh | bash
%end
