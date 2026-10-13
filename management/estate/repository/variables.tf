variable "name" {
  description = "The repository, as its organisation knows it."
  type        = string
}

variable "delivers_unreviewed" {
  description = "The App that brings a pin or a delivery to the branch, as the platform numbers it: the one party that may pass the review by."
  type        = number
}
