variable "account_id" {
  type        = string
  description = "The account the watchman runs in."
}

variable "sites" {
  type        = set(string)
  description = "The key of every site the watchman listens for. Keys and not names: a key is in git and in every plan."
}

variable "workers_subdomain" {
  type        = string
  description = "The account's own name under workers.dev, which is where the watchman is reached. Chosen once in the vendor's console and kept in the estate's vault."
}

variable "channel" {
  type        = string
  sensitive   = true
  description = "The webhook of the channel every site's alerts reach, which is where a site going quiet is said."
}
