variable "proxmox_lifecycle_smoke" {
  type        = bool
  default     = false
  description = "Clone only Charlie's internet Fedora VM in Proxmox; keep the rest simulated."
}

variable "proxmox_test_deployment" {
  type        = bool
  default     = false
  description = "Provision all four test-project VMs, their isolated Proxmox networks, and NIC attachments in Proxmox."
}
