variable "name" {
  description = "The group, as the runners that join it name it."
  type        = string
}

variable "repositories" {
  description = "The repositories that may use the group, as the platform numbers them."
  type        = set(number)
}

variable "workflows" {
  description = "The only workflows that may use the group, each as <organisation>/<repository>/<path>@<ref>."
  type        = list(string)
}
