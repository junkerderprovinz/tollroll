// Package fetch reads the status contract from one app: its report and its
// log, over HTTPS with a read token or over a unix socket on this server.
//
// Apps on a LAN mostly show a self-signed certificate. Such a certificate is
// trusted on first use: the first one an app shows is handed to the learn
// callback, and from then on only that one is accepted. An app whose first
// certificate the system trusts for its host name is pinned as PinSystem and
// from then on has to show one the system trusts.
package fetch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/junkerderprovinz/parleyport/kit/status"

	"github.com/junkerderprovinz/tollroll/internal/sources"
)

// maxBody is far above the contract's 64 KiB report and a full log page, and
// keeps a misbehaving app from filling the memory.
const maxBody = 1 << 20

const timeout = 5 * time.Second

// Error says why an app could not be read. Code is one of unreachable,
// unauthorized, forbidden, not_found, bad_answer, cert_changed and tls; the
// settings page and the tile translate it.
type Error struct {
	Code string
	err  error
}

func (e *Error) Error() string {
	if e.err != nil {
		return e.Code + ": " + e.err.Error()
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.err }

var errCertChanged = errors.New("the certificate differs from the one learned")

// PinSystem is the pin of an app whose certificate a public authority
// vouches for.
const PinSystem = "system"

// Client reads apps. It is safe for concurrent use.
type Client struct {
	learn func(id, address, pin string)
	roots *x509.CertPool
	dial  func(ctx context.Context, network, addr string) (net.Conn, error)
}

// New returns a client that hands every certificate it trusts on first use
// to learn, along with the source's ID and address. learn may be nil.
func New(learn func(id, address, pin string)) *Client {
	return &Client{learn: learn}
}

// Report reads the app's report, with its texts in lang where the app has
// them.
func (c *Client) Report(ctx context.Context, src sources.Source, lang string) (status.Report, error) {
	q := url.Values{}
	if lang != "" {
		q.Set("lang", lang)
	}
	var rep status.Report
	if err := c.get(ctx, src, status.StatusPath, q, &rep); err != nil {
		return status.Report{}, err
	}
	if rep.Contract != status.Contract || rep.App.ID == "" || rep.App.Name == "" {
		return status.Report{}, &Error{Code: "bad_answer", err: fmt.Errorf("contract %d, app %q", rep.Contract, rep.App.ID)}
	}
	return rep, nil
}

// Log reads up to limit log entries after the cursor.
func (c *Client) Log(ctx context.Context, src sources.Source, after, lang string, limit int) (status.LogPage, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if after != "" {
		q.Set("after", after)
	}
	if lang != "" {
		q.Set("lang", lang)
	}
	var page status.LogPage
	if err := c.get(ctx, src, status.LogPath, q, &page); err != nil {
		return status.LogPage{}, err
	}
	return page, nil
}

func (c *Client) get(ctx context.Context, src sources.Source, path string, q url.Values, into any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var seen string
	hc, target := c.client(src, &seen)
	u := target + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return &Error{Code: "unreachable", err: err}
	}
	req.Header.Set("Accept", "application/json")
	if src.Kind() == sources.KindURL && src.Token != "" {
		req.Header.Set("Authorization", "Bearer "+src.Token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return classify(err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return &Error{Code: "unauthorized"}
	case http.StatusForbidden:
		return &Error{Code: "forbidden"}
	case http.StatusNotFound:
		return &Error{Code: "not_found"}
	default:
		return &Error{Code: "bad_answer", err: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return &Error{Code: "unreachable", err: err}
	}
	if len(body) > maxBody {
		return &Error{Code: "bad_answer", err: errors.New("answer too large")}
	}
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(into); err != nil {
		return &Error{Code: "bad_answer", err: err}
	}
	if seen != "" && c.learn != nil {
		c.learn(src.ID, src.Address, seen)
	}
	return nil
}

// client builds the HTTP client for one call. A redirect is never followed,
// so the token goes nowhere but the address that was saved.
func (c *Client) client(src sources.Source, seen *string) (*http.Client, string) {
	tr := &http.Transport{
		ResponseHeaderTimeout: timeout,
		TLSHandshakeTimeout:   timeout,
		DisableKeepAlives:     true,
	}
	target := src.Address
	if src.Kind() == sources.KindSocket {
		sock := src.Address
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		}
		target = "http://app"
	} else {
		tr.DialContext = c.dial
		host := ""
		if u, err := url.Parse(src.Address); err == nil {
			host = u.Hostname()
		}
		tr.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			// The chain is checked in VerifyConnection, against the system
			// roots first and the learned pin second.
			InsecureSkipVerify: true, // #nosec G402
			VerifyConnection: func(cs tls.ConnectionState) error {
				return c.verify(cs, host, src.Pin, seen)
			},
		}
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, target
}

func (c *Client) verify(cs tls.ConnectionState, host, pin string, seen *string) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("no certificate")
	}
	leaf := cs.PeerCertificates[0]
	inter := x509.NewCertPool()
	for _, ic := range cs.PeerCertificates[1:] {
		inter.AddCert(ic)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: c.roots, DNSName: host, Intermediates: inter}); err == nil {
		if pin == "" {
			*seen = PinSystem
		}
		return nil
	}
	sum := sha256.Sum256(leaf.Raw)
	fp := hex.EncodeToString(sum[:])
	switch pin {
	case fp:
		return nil
	case "":
		*seen = fp
		return nil
	}
	return errCertChanged
}

func classify(err error) error {
	if errors.Is(err, errCertChanged) {
		return &Error{Code: "cert_changed", err: err}
	}
	var (
		ve *tls.CertificateVerificationError
		re tls.RecordHeaderError
		ae tls.AlertError
	)
	if errors.As(err, &ve) || errors.As(err, &re) || errors.As(err, &ae) {
		return &Error{Code: "tls", err: err}
	}
	return &Error{Code: "unreachable", err: err}
}
