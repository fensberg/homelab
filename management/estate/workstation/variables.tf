variable "account_id" {
  type        = string
  description = "The account the tunnel is made in."
}

variable "estate" {
  type        = string
  description = "The estate's name as a slug, which leads the tunnel's name."
}

variable "name" {
  type        = string
  description = "What the tunnel is for, which ends its name and is the comment on its route."
}

variable "address" {
  type        = string
  description = "The workstation's own address: the one thing the tunnel routes. One IPv4 address, which the caller checks before anything is granted."
}
