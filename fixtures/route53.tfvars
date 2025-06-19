route53 = {
  domains = [{
    enable_dnssec = true
    name          = "example.org"
    records = [{
      name    = "test"
      records = "1.1.1.1"
      ttl     = 95
      type    = "A"
    }]
    }, {
    name = "example.test"
    records = [{
      name    = ""
      records = "2.2.2.2"
      ttl     = 95
      type    = "A"
      }, {
      name    = "example"
      records = "example.org"
      ttl     = 95
      type    = "CNAME"
    }]
  }]
}
