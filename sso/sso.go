package sso

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/gorilla/csrf"
	"github.com/vulcand/oxy/forward"
)

var log *logrus.Logger

func init() {
	log = logrus.New()
	log.Level = logrus.InfoLevel
	f := new(logrus.TextFormatter)
	f.TimestampFormat = "2006-01-02 15:04:05"
	f.FullTimestamp = true
	log.Formatter = f
}

type SSO struct {
	UpstreamURL   *url.URL
	APIURL        *url.URL
	AppPublicURL  *url.URL
	StaticPath    string
	TemplatePath  string
	EncryptionKey []byte
	CSRFAuthKey   []byte
	Authorized    func(User) (bool, error)
	template      *template.Template
	templateOnce  sync.Once
}

type User struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Login         string `json:"login"`
	Email         string `json:"email"`
	GravatarID    string `json:"gravatar_id"`
	IsSyncing     bool   `json:"is_syncing"`
	SyncedAt      string `json:"synced_at"`
	CorrectScopes bool   `json:"correct_scopes"`
	CreatedAt     string `json:"created_at"`
}

type APIMessage struct {
	User User `json:"user"`
}

type State struct {
	User  User   `json:"user"`
	Token string `json:"token"`
}

// New creates a sso middleware.
func New(sso *SSO) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return sso
	}
}

func (sso *SSO) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	log.Println("ServeHTTP")

	// TODO: HSTS
	if sso.AppPublicURL.Scheme == "https" && req.URL.Scheme != "https" && req.Header.Get("x-forwarded-proto") != "https" {
		http.Redirect(w, req, sso.AppPublicURL.String(), http.StatusFound)
		return
	}

	mux := http.NewServeMux()
	mux.Handle("/sso/static/", sso.handleStatic(w, req))
	mux.HandleFunc("/favicon.ico", sso.handleEmpty)
	mux.HandleFunc("/sso/login", sso.handleLogin)
	mux.HandleFunc("/sso/logout", sso.handleLogout)
	mux.HandleFunc("/", http.HandlerFunc(sso.handleRequest))

	server := csrf.Protect(
		sso.CSRFAuthKey,
		csrf.FieldName("authenticity_token"),
		csrf.Path("/"),
		csrf.Domain(domainFromHost(sso.AppPublicURL.Host)),
		csrf.Secure(sso.AppPublicURL.Scheme == "https"),
	)(mux)
	server.ServeHTTP(w, req)
}

func (sso *SSO) handleEmpty(w http.ResponseWriter, req *http.Request) {
	w.WriteHeader(204)
}

func (sso *SSO) handleStatic(w http.ResponseWriter, req *http.Request) http.Handler {
	return http.StripPrefix("/sso/static/", http.FileServer(http.Dir(sso.StaticPath)))
}

func (sso *SSO) handleRequest(w http.ResponseWriter, req *http.Request) {
	log.Println("handleRequest")

	state, err := sso.stateFromRequest(req)
	if err != nil && err != http.ErrNoCookie {
		// decoding state failed
		// could be an issue with the cookie, remove it
		sso.setLogoutCookie(w)
		http.Error(w, err.Error(), 500)
		return
	}

	if state != nil {
		// we have a state => we are authenticated
		sso.handleProxy(w, req, state)
		return
	}

	sso.handleHandshake(w, req)
}

func (sso *SSO) handleProxy(w http.ResponseWriter, req *http.Request, state *State) {
	log.Println("handleProxy")

	b, err := json.Marshal(state)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	req.URL.Scheme = sso.UpstreamURL.Scheme
	req.URL.Host = sso.UpstreamURL.Host
	req.Header.Add("Travis-State", string(b))

	fwd, _ := forward.New()
	fwd.ServeHTTP(w, req)
}

func (sso *SSO) handleLogin(w http.ResponseWriter, req *http.Request) {
	log.Println("handleLogin")

	token := ""
	if req.Method == "POST" {
		token = req.FormValue("sso_token")
	}

	if token == "" {
		log.Println("no token found, try again")
		http.Redirect(w, req, "/", http.StatusFound)
		return
	}

	url := *sso.APIURL

	q := url.Query()
	q.Add("access_token", token)

	url.Path = "/users"
	url.RawQuery = q.Encode()

	apiReq, err := http.NewRequest("GET", url.String(), nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	client := &http.Client{}

	apiReq.Header.Add("Accept", "application/vnd.travis-ci.2+json")
	apiResp, err := client.Do(apiReq)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer apiResp.Body.Close()

	if apiResp.StatusCode != http.StatusOK {
		content, _ := ioutil.ReadAll(apiResp.Body)
		http.Error(w, fmt.Sprintf("upstream error, code=%v, body=%v\n", apiResp.StatusCode, string(content)), 500)
		return
	}

	var m APIMessage
	err = json.NewDecoder(apiResp.Body).Decode(&m)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	user := m.User

	ok, err := sso.Authorized(user)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	if !ok {
		http.Error(w, fmt.Sprintf("access denied for user %s", user.Login), 403)
		return
	}

	state := &State{
		User:  user,
		Token: token,
	}

	b, err := json.Marshal(state)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	encryptedCookie, nonce, err := encrypt(b, sso.EncryptionKey)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	encryptedCookie = append(nonce, encryptedCookie...)
	encodedCookie := base64.StdEncoding.EncodeToString(encryptedCookie)

	http.SetCookie(w, &http.Cookie{
		Name:    "travis.sso",
		Value:   encodedCookie,
		Path:    "/",
		Domain:  domainFromHost(sso.AppPublicURL.Host),
		Expires: time.Now().Add(365 * 24 * time.Hour),
	})

	log.Println("cookies set, redirecting back")

	http.Redirect(w, req, "/", http.StatusFound)
}

func (sso *SSO) handleHandshake(w http.ResponseWriter, req *http.Request) {
	log.Println("handleHandshake")

	if req.Method != "GET" && req.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, fmt.Sprintf(`must be <a href="%s">GET</a> request`, req.URL), 405)
		return
	}

	log.Println("about to call once")

	sso.templateOnce.Do(func() {
		var err error
		sso.template, err = template.ParseFiles(sso.TemplatePath + "/login.html")
		if err != nil {
			log.Fatalf("error compiling template: %v", err)
		}
	})

	sso.template.Execute(w, map[string]interface{}{
		"Public":   "/sso/static",
		"Endpoint": sso.APIURL.String(),
		"Origin":   sso.AppPublicURL.String(),
		"CSRF":     csrf.Token(req),
	})
}

func (sso *SSO) handleLogout(w http.ResponseWriter, req *http.Request) {
	log.Println("handleLogout")

	if req.Method != "POST" {
		w.Header().Add("Content-Type", "text/html; encoding=UTF-8")
		w.Write([]byte(`<form method="POST" action="/sso/logout">`))
		w.Write([]byte(`<input type="hidden" name="authenticity_token" value="`))
		w.Write([]byte(html.EscapeString(csrf.Token(req))))
		w.Write([]byte(`">`))
		w.Write([]byte(`<input type="submit" value="logout">`))
		w.Write([]byte(`</form>`))
		return
	}

	sso.setLogoutCookie(w)

	w.Write([]byte("logged out"))
}

func (sso *SSO) stateFromRequest(req *http.Request) (*State, error) {
	cookie, err := req.Cookie("travis.sso")
	if err == http.ErrNoCookie {
		return nil, http.ErrNoCookie
	}
	if err != nil {
		return nil, err
	}

	decodedCookie, err := base64.StdEncoding.DecodeString(cookie.Value)
	if err != nil {
		return nil, err
	}

	encryptedCookie := []byte(decodedCookie)

	nonce := encryptedCookie[:12]
	encryptedCookie = encryptedCookie[12:]

	if len(nonce) != 12 {
		return nil, errors.New("nonce must be 12 characters in length")
	}

	if len(encryptedCookie) == 0 {
		return nil, errors.New("encrypted cookie missing")
	}

	b, err := decrypt(encryptedCookie, nonce, sso.EncryptionKey)
	if err != nil {
		return nil, err
	}

	var state *State
	err = json.NewDecoder(bytes.NewReader(b)).Decode(&state)
	if err != nil {
		return nil, err
	}

	return state, nil
}

func (sso *SSO) setLogoutCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:    "travis.sso",
		Value:   "",
		Path:    "/",
		Domain:  domainFromHost(sso.AppPublicURL.Host),
		Expires: time.Date(1970, time.January, 1, 1, 0, 0, 0, time.UTC),
	})
}

func domainFromHost(host string) string {
	index := strings.Index(host, ":")
	if index > 0 {
		return host[:index]
	}
	return host
}

// https://gist.github.com/kkirsche/e28da6754c39d5e7ea10

func encrypt(plaintext, key []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}

	// Never use more than 2^32 random nonces with a given key because of the risk of a repeat.
	nonce := make([]byte, 12)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}

	return aesgcm.Seal(nil, nonce, plaintext, nil), nonce, nil
}

func decrypt(ciphertext, nonce, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	plaintext, err := aesgcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}

	return plaintext, nil
}
