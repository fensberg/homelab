variable "name" {
  description = "The repository, as its organisation knows it."
  type        = string
}

variable "default_branch" {
  description = "The branch the estate is built from: the only one a job may be handed the estate's credential on."
  type        = string
}

variable "pull_request_runners_site" {
  description = "The site whose pools a pull request's jobs run in, by its key."
  type        = string
}

variable "delivers_unreviewed" {
  description = "The App that brings a pin or a delivery to the branch, as the platform numbers it: the one party that may pass the review by."
  type        = number
}
