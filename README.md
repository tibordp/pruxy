# pruxy

pruxy is a proxy server for Prusa Link printers (e.g. MK4) that exposes the printer's API without authentication. The use case is to run this in a trusted local network
or where the pruxy itself is protected by something like oauth2_proxy.

In addition it provides /metrics endpoint for Prometheus monitoring because why not.

Only tested on MK4, since that's what I have.

## Usage

```bash
go mod download
go build ./pruxy.go
./pruxy --bind :8080 --printer http://192.168.0.54 --username maker --password nf8OPmfnw3mKlda
```
