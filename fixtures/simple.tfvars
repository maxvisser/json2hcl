instance_type      = "t2.micro"
instance_count     = 3
availability_zones = ["us-west-1a", "us-west-1c"]
tags = {
  environment = "dev"
  project     = "web-app"
}
