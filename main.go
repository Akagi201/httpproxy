package main

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/Akagi201/light"
	"github.com/gohttp/logger"
	flags "github.com/jessevdk/go-flags"
	"github.com/vulcand/oxy/forward"
	"github.com/vulcand/oxy/testutils"
)

var opts struct {
	ListenAddr   string `long:"listen" default:"0.0.0.0:8888" description:"address and port to listen on"`
	UpstreamURL  string `long:"upstream" default:"" description:"upstream url. e.g.: http://127.0.0.1:8899"`
	StaticPath   string `long:"static" default:"./static" description:"path to static files"`
	TemplatePath string `long:"template" default:"./template" description:"path to template files"`
}

func main() {
	_, err := flags.Parse(&opts)
	if err != nil {
		if !strings.Contains(err.Error(), "Usage") {
			log.Fatalf("error: %v\n", err.Error())
		} else {
			return
		}
	}

	app := light.New()
	app.Use(logger.New())

	fwd, err := forward.New()
	if err != nil {
		log.Fatalf("error: %v\n", err.Error())
	}

	redirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// let us forward this request to another server
		r.URL = testutils.ParseURI(opts.UpstreamURL)
		fwd.ServeHTTP(w, r)
	})

	app.Handle("/", redirect)

	log.Printf("HTTP listening at: %v", opts.ListenAddr)
	app.Listen(fmt.Sprintf("%v", opts.ListenAddr))
}
