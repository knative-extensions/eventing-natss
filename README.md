# NATS - Go Client
A [Go](http://golang.org) client for the [NATS messaging system](https://nats.io).

[![License Apache 2](https://img.shields.io/badge/License-Apache2-blue.svg)](https://www.apache.org/licenses/LICENSE-2.0)
[![FOSSA Status](https://app.fossa.io/api/projects/git%2Bgithub.com%2Fnats-io%2Fgo-nats.svg?type=shield)](https://app.fossa.io/projects/git%2Bgithub.com%2Fnats-io%2Fgo-nats?ref=badge_shield)
[![Go Report Card](https://goreportcard.com/badge/github.com/nats-io/nats.go)](https://goreportcard.com/report/github.com/nats-io/nats.go) [![Build Status](https://travis-ci.com/nats-io/nats.go.svg?branch=master)](http://travis-ci.com/nats-io/nats.go) [![GoDoc](https://img.shields.io/badge/GoDoc-reference-007d9c)](https://pkg.go.dev/github.com/nats-io/nats.go)
[![Coverage Status](https://coveralls.io/repos/nats-io/nats.go/badge.svg?branch=master)](https://coveralls.io/r/nats-io/nats.go?branch=master)
[![License Apache 2][License-Image]][License-Url] [![Go Report Card][ReportCard-Image]][ReportCard-Url] [![Build Status][Build-Status-Image]][Build-Status-Url] [![GoDoc][GoDoc-Image]][GoDoc-Url] [![Coverage Status][Coverage-image]][Coverage-Url]

[License-Url]: https://www.apache.org/licenses/LICENSE-2.0
[License-Image]: https://img.shields.io/badge/License-Apache2-blue.svg
[ReportCard-Url]: https://goreportcard.com/report/github.com/nats-io/nats.go
[ReportCard-Image]: https://goreportcard.com/badge/github.com/nats-io/nats.go
[Build-Status-Url]: https://travis-ci.com/github/nats-io/nats.go
[Build-Status-Image]: https://travis-ci.com/nats-io/nats.go.svg?branch=main
[GoDoc-Url]: https://pkg.go.dev/github.com/nats-io/nats.go
[GoDoc-Image]: https://img.shields.io/badge/GoDoc-reference-007d9c
[Coverage-Url]: https://coveralls.io/r/nats-io/nats.go?branch=main
[Coverage-image]: https://coveralls.io/repos/github/nats-io/nats.go/badge.svg?branch=main

## Installation


```bash
# Go client latest or explicit version
go get github.com/nats-io/nats.go/@latest
go get github.com/nats-io/nats.go/@v1.15.0
go get github.com/nats-io/nats.go/@v1.15.0

# For latest NATS Server, add /v2 at the end
go get github.com/nats-io/nats-server/v2
