variable "cyber_environment_network" {
  type        = string
  default     = "cyber.lab"
  description = "Validated platform network used by each f1 and the shared router's WAN."
}

variable "shared_router_egress_enabled" {
  type        = bool
  default     = true
  description = "Attach the shared router to cyber.lab to provide egress for the shared VNet."
}

variable "router_egress_ipv4_method" {
  type        = string
  default     = "static"
  description = "WAN address method for the shared router. Static mode consumes one address from cyber_environment_network; DHCP uses that network's DHCP service."

  validation {
    condition     = contains(["dhcp", "static"], var.router_egress_ipv4_method)
    error_message = "Router egress IPv4 method must be dhcp or static."
  }
}

variable "shared_router_dhcp_range" {
  type = object({
    start = string
    end   = string
  })
  default = {
    start = "192.168.1.30"
    end   = "192.168.1.40"
  }
  description = "DHCP host range for the single shared VNet router."
}

variable "private_router_dhcp_range" {
  type = object({
    start = string
    end   = string
  })
  default = {
    start = "192.168.2.30"
    end   = "192.168.2.40"
  }
  description = "DHCP host range used independently by every per-student private router."
}

variable "router_lan_dns_servers" {
  type        = list(string)
  default     = ["192.168.1.1"]
  description = "DNS addresses advertised by the shared router to its DHCP clients."
}

variable "private_router_dns_servers" {
  type        = list(string)
  default     = ["192.168.2.1"]
  description = "DNS addresses advertised by each private router to its DHCP clients."
}
