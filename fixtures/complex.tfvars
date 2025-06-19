vpc = {
  cidr_block           = "10.0.0.0/16"
  enable_dns_hostnames = true
  enable_dns_support   = true
}
subnets = [{
  availability_zone       = "us-west-2a"
  cidr_block              = "10.0.1.0/24"
  map_public_ip_on_launch = true
  name                    = "public-1"
  }, {
  availability_zone       = "us-west-2b"
  cidr_block              = "10.0.2.0/24"
  map_public_ip_on_launch = true
  name                    = "public-2"
  }, {
  availability_zone       = "us-west-2a"
  cidr_block              = "10.0.10.0/24"
  map_public_ip_on_launch = false
  name                    = "private-1"
}]
security_groups = [{
  description = "Web server security group"
  ingress_rules = [{
    cidr_blocks = ["0.0.0.0/0"]
    from_port   = 80
    protocol    = "tcp"
    to_port     = 80
    }, {
    cidr_blocks = ["0.0.0.0/0"]
    from_port   = 443
    protocol    = "tcp"
    to_port     = 443
  }]
  name = "web"
}]
