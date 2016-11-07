package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/Akagi201/httpproxy/sso"
	"github.com/Akagi201/light"
	mlogrus "github.com/Akagi201/middleware/logrus"
	"github.com/Sirupsen/logrus"
	flags "github.com/jessevdk/go-flags"
)

var opts struct {
	ListenAddr    string   `long:"listen" default:"0.0.0.0:8080" description:"address and port to listen on"`
	UpstreamURL   string   `long:"upstream" default:"" description:"upstream url. e.g.: http://127.0.0.1:8899"`
	APIURL        string   `long:"api" default:"https://api.github.com" description:"GitHub API url"`
	AppPublicURL  string   `long:"app" default:"" description:"APP public url"`
	StaticPath    string   `long:"static" default:"./static" description:"path to static files"`
	TemplatePath  string   `long:"template" default:"./template" description:"path to template files"`
	EncryptionKey string   `long:"encrypt" default:"" description:"key used for cookie authenticated encryption (32 chars)"`
	CSRFAuthKey   string   `long:"csrf" default:"" description:"key used for cookie authenticated encryption (32 chars)"`
	AuthUsers     []string `long:"auth" description:"list of users that are authorized to use the app"`
}

var log *logrus.Logger

func init() {
	log = logrus.New()
	log.Level = logrus.InfoLevel
	f := new(logrus.TextFormatter)
	f.TimestampFormat = "2006-01-02 15:04:05"
	f.FullTimestamp = true
	log.Formatter = f
}

func isDir(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}

	return fi.Mode().IsDir(), nil
}

func main() {
	_, err := flags.Parse(&opts)
	if err != nil {
		if !strings.Contains(err.Error(), "Usage") {
			log.Fatalf("error: %v", err.Error())
		} else {
			return
		}
	}

	if opts.UpstreamURL == "" {
		log.Fatalln("missing upstream url")
	}

	upstreamURL, err := url.Parse(opts.UpstreamURL)
	if err != nil {
		log.Fatalf("invalid upstream url: %v", err)
	}

	apiURL, err := url.Parse(opts.APIURL)
	if err != nil {
		log.Fatalf("invalid api url: %v", err)
	}

	if opts.AppPublicURL == "" {
		log.Fatalln("missing app public url")
	}

	appPublicURL, err := url.Parse(opts.AppPublicURL)
	if err != nil {
		log.Fatalf("invalid app public url: %v", err)
	}

	if ok, err := isDir(opts.StaticPath); err != nil || !ok {
		log.Fatalf("invalid static path %v: %v", opts.StaticPath, err)
	}

	if ok, err := isDir(opts.TemplatePath); err != nil || !ok {
		log.Fatalf("invalid template path %v: %v", opts.TemplatePath, err)
	}

	if opts.EncryptionKey == "" || len(opts.EncryptionKey) != 32 {
		log.Fatalln("invalid encryption key: length must be exactly 32 bytes")
	}

	if opts.CSRFAuthKey == "" || len(opts.CSRFAuthKey) != 32 {
		log.Fatalln("invalid csrf key: length must be exactly 32 bytes")
	}

	if len(opts.AuthUsers) == 0 {
		log.Fatalln("missing authorized-users")
	}

	authorized := make(map[string]bool)
	for _, v := range opts.AuthUsers {
		authorized[v] = true
	}

	log.Printf("Parsed args, upstream: %v, api_url: %v, app public url: %v, static: %v, template: %v, encryption key: %v, csrf key: %v, auth users: %v",
		upstreamURL, apiURL, appPublicURL, opts.StaticPath, opts.TemplatePath, opts.EncryptionKey, opts.CSRFAuthKey, authorized)

	app := light.New()
	app.Use(mlogrus.New())

	s := &sso.SSO{
		UpstreamURL:   upstreamURL,
		APIURL:        apiURL,
		AppPublicURL:  appPublicURL,
		StaticPath:    opts.StaticPath,
		TemplatePath:  opts.TemplatePath,
		EncryptionKey: []byte(opts.EncryptionKey),
		CSRFAuthKey:   []byte(opts.CSRFAuthKey),
		Authorized: func(u sso.User) (bool, error) {
			return authorized[u.Login], nil
		},
	}

	app.Use(sso.New(s))

	//app.Handle("/", sso)

	log.Printf("HTTP listening at: %v", opts.ListenAddr)
	app.Listen(fmt.Sprintf("%v", opts.ListenAddr))
}
