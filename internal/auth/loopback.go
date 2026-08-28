package auth

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// CallbackResult is delivered once by a Loopback listener.
type CallbackResult struct {
	Code string
	Err  error
}

// Loopback listens on the exact IP-loopback redirect URI registered with FreeAgent.
type Loopback struct {
	registered *url.URL
	state      string
	listener   net.Listener
	server     *http.Server
	result     chan CallbackResult
	closeOnce  sync.Once
}

// ListenLoopback binds the exact host and port in registeredRedirectURI. FreeAgent
// requires the redirect URI to match the application's registered value.
func ListenLoopback(registeredRedirectURI, expectedState string) (*Loopback, error) {
	if expectedState == "" {
		return nil, errors.New("expected OAuth state is required")
	}
	registered, err := url.Parse(registeredRedirectURI)
	if err != nil || !registered.IsAbs() {
		return nil, errors.New("valid registered redirect URI is required")
	}
	if registered.Scheme != "http" || registered.RawQuery != "" || registered.Fragment != "" || registered.RawPath != "" || registered.User != nil || registered.Path == "" {
		return nil, errors.New("loopback redirect URI must be a plain HTTP URL without a query or fragment")
	}
	ip := net.ParseIP(registered.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("loopback redirect URI must use an IP loopback host")
	}
	if registered.Port() == "" {
		return nil, errors.New("loopback redirect URI must contain an explicit port")
	}
	listener, err := net.Listen("tcp", registered.Host)
	if err != nil {
		return nil, fmt.Errorf("listen on OAuth redirect URI: %w", err)
	}
	loopback := &Loopback{
		registered: registered,
		state:      expectedState,
		listener:   listener,
		result:     make(chan CallbackResult, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc(registered.EscapedPath(), loopback.handle)
	loopback.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: defaultCallbackHeaderTimeout,
		ReadTimeout:       defaultCallbackHeaderTimeout,
		WriteTimeout:      defaultCallbackHeaderTimeout,
		IdleTimeout:       defaultCallbackHeaderTimeout,
		MaxHeaderBytes:    8 << 10,
	}
	go func() {
		if serveErr := loopback.server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			loopback.deliver(CallbackResult{Err: fmt.Errorf("serve OAuth callback: %w", serveErr)})
		}
	}()
	return loopback, nil
}

const defaultCallbackHeaderTimeout = 10 * time.Second

func (l *Loopback) handle(response http.ResponseWriter, request *http.Request) {
	remoteHost, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil || !net.ParseIP(remoteHost).IsLoopback() {
		http.Error(response, "OAuth callback rejected", http.StatusForbidden)
		return
	}
	if request.Method != http.MethodGet || request.Host != l.registered.Host || request.URL.EscapedPath() != l.registered.EscapedPath() {
		http.Error(response, "OAuth callback does not match the registered redirect URI", http.StatusBadRequest)
		return
	}
	callback := l.registered.Scheme + "://" + request.Host + request.URL.RequestURI()
	code, parseErr := ParseCallbackURL(callback, l.registered.String(), l.state)
	if parseErr != nil {
		if errors.Is(parseErr, ErrStateMismatch) {
			// A stray or malicious request must not terminate the real flow.
			http.Error(response, "OAuth state did not match", http.StatusBadRequest)
			return
		}
		l.deliver(CallbackResult{Err: parseErr})
		writeCallbackPage(response, false)
		return
	}
	l.deliver(CallbackResult{Code: code})
	writeCallbackPage(response, true)
}

var callbackPage = template.Must(template.New("callback").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><title>FreeAgent CLI</title><body><p>{{.}}</p></body></html>`))

func writeCallbackPage(response http.ResponseWriter, success bool) {
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	message := "FreeAgent authorization was not completed. Return to the terminal."
	if success {
		message = "FreeAgent authorization is complete. You can close this window."
	}
	_ = callbackPage.Execute(response, message)
}

func (l *Loopback) deliver(result CallbackResult) {
	select {
	case l.result <- result:
	default:
	}
}

// Wait waits for one valid callback, denial, server failure, or context cancellation.
func (l *Loopback) Wait(ctx context.Context) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-l.result:
		return result.Code, result.Err
	}
}

// Close stops the listener. It is safe to call more than once.
func (l *Loopback) Close() error {
	var closeErr error
	l.closeOnce.Do(func() {
		closeErr = l.server.Close()
		if errors.Is(closeErr, http.ErrServerClosed) || strings.Contains(fmt.Sprint(closeErr), "use of closed network connection") {
			closeErr = nil
		}
	})
	return closeErr
}
