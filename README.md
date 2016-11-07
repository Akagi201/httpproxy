# httpproxy

HTTP proxy with sso auth and https support in Go.

## Features
- [x] Use [light](https://github.com/Akagi201/light) as http framework.
- [x] Support http proxy.
- [ ] Support https proxy to http.
- [ ] Support websocket proxy.
- [ ] Support GitHub SSO.

## Build
* docker: `docker build -t httpproxy .`
* `go build -o httpproxy`

## Run
* `httpproxy -h` for help
* `./httpproxy --upstream="http://localhost:8081" --app="http://localhost:8080" --encrypt="sa8OoLei6eWiezah9ohk8Wah6Ow6pee9" --csrf="oxei9aebonogh1Gaina4ePaitheechei" --auth="Akagi201"`
