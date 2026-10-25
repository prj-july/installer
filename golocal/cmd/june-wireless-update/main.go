// june-wireless-update is a small client for the authenticated update API
// exposed by an installed June Local oven. It never uses ADB.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type reply struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Update  struct {
		Enabled        bool   `json:"enabled"`
		State          string `json:"state"`
		Channel        string `json:"channel"`
		CurrentRelease string `json:"current_release"`
		LastError      string `json:"last_error"`
		Available      *struct {
			Release string `json:"release"`
			Notes   string `json:"notes"`
		} `json:"available"`
		Pending *struct {
			Release string `json:"release"`
		} `json:"pending"`
	} `json:"update"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	oven := flag.String("oven", "", "oven base URL, for example https://192.168.1.100")
	tokenFile := flag.String("token-file", "", "file containing the per-install update control token")
	caFile := flag.String("ca", "", "trust an additional oven CA certificate (PEM)")
	jsonOut := flag.Bool("json", false, "print the server's JSON reply")
	yes := flag.Bool("yes", false, "stage without an interactive confirmation")
	timeout := flag.Duration("timeout", 20*time.Minute, "request timeout")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s -oven URL [-token-file FILE] [status|check|stage] [-yes]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	command := "status"
	if flag.NArg() > 0 {
		command = flag.Arg(0)
	}
	if flag.NArg() > 1 {
		return errors.New("only one command may be supplied")
	}
	if *oven == "" {
		return errors.New("-oven is required")
	}
	base, err := url.Parse(*oven)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil {
		return errors.New("-oven must be an HTTPS URL without embedded credentials")
	}
	base.Path = strings.TrimRight(base.Path, "/")
	path := "/local/update"
	if command == "check" || command == "stage" {
		path += "/" + command
	} else if command != "status" {
		return fmt.Errorf("unknown command %q", command)
	}

	method := http.MethodGet
	token := ""
	if command != "status" {
		method = http.MethodPost
		if !*yes && command == "stage" {
			fmt.Fprint(os.Stderr, "Stage this signed update for the next boot? [y/N] ")
			var answer string
			_, _ = fmt.Fscanln(os.Stdin, &answer)
			if !strings.EqualFold(answer, "y") && !strings.EqualFold(answer, "yes") {
				return errors.New("cancelled")
			}
		}
		token, err = loadToken(*tokenFile)
		if err != nil {
			return err
		}
	}

	client, err := httpClient(*caFile, *timeout)
	if err != nil {
		return err
	}
	u := *base
	u.Path = strings.TrimRight(base.Path, "/") + path
	req, err := http.NewRequest(method, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "june-wireless-update/1")
	if token != "" {
		req.Header.Set("X-June-Update-Token", token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if *jsonOut {
		_, _ = os.Stdout.Write(body)
		if len(body) > 0 && body[len(body)-1] != '\n' {
			fmt.Println()
		}
	} else {
		printReply(body)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var r reply
		_ = json.Unmarshal(body, &r)
		if r.Error != "" {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, r.Error)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func loadToken(path string) (string, error) {
	if path == "" {
		path = os.Getenv("JUNE_UPDATE_TOKEN_FILE")
	}
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if token := strings.TrimSpace(string(b)); token != "" {
			return token, nil
		}
	}
	if token := strings.TrimSpace(os.Getenv("JUNE_UPDATE_TOKEN")); token != "" {
		return token, nil
	}
	return "", errors.New("update token required: use -token-file or JUNE_UPDATE_TOKEN")
}

func httpClient(caFile string, timeout time.Duration) (*http.Client, error) {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("CA file did not contain a PEM certificate")
		}
	}
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return errors.New("update API redirects are not allowed")
		},
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    roots,
		}},
	}, nil
}

func printReply(body []byte) {
	var r reply
	if err := json.Unmarshal(body, &r); err != nil {
		fmt.Println(string(body))
		return
	}
	u := r.Update
	fmt.Printf("state:   %s\n", text(u.State))
	if u.Channel != "" {
		fmt.Printf("channel: %s\n", u.Channel)
	}
	if u.CurrentRelease != "" {
		fmt.Printf("current: %s\n", u.CurrentRelease)
	}
	if u.Pending != nil && u.Pending.Release != "" {
		fmt.Printf("pending: %s (power-cycle the oven to apply)\n", u.Pending.Release)
	}
	if u.Available != nil && u.Available.Release != "" {
		fmt.Printf("available: %s\n", u.Available.Release)
		if u.Available.Notes != "" {
			fmt.Printf("notes: %s\n", u.Available.Notes)
		}
	}
	if u.LastError != "" {
		fmt.Printf("error: %s\n", u.LastError)
	}
}

func text(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
