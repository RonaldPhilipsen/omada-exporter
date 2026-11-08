package omadaapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/RonaldPhilipsen/omada-exporter/internal/config"
	"github.com/RonaldPhilipsen/omada/cloud"
)

const testSecret = "s3cr3t-value"

// newTestClient starts a TLS server. The handler gets every non-token request.
func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/openapi/authorize/token" {
			fmt.Fprint(w, `{"errorCode":0,"result":{"accessToken":"tok","tokenType":"bearer","expiresIn":7200,"refreshToken":"r"}}`)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	c, err := New(config.Config{
		BaseURL: srv.URL, OmadacID: "oid", ClientID: "cid",
		ClientSecret: testSecret, InsecureTLS: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// gridHandler serves rows named by page for an arbitrary total.
func gridHandler(total, perPage int, row func(i int) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		size, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
		if size > perPage {
			size = perPage
		}
		var rows []string
		for i := (page - 1) * size; i < page*size && i < total; i++ {
			rows = append(rows, row(i))
		}
		fmt.Fprintf(w, `{"errorCode":0,"result":{"totalRows":%d,"currentPage":%d,"currentSize":%d,"data":[%s]}}`,
			total, page, size, strings.Join(rows, ","))
	}
}

func TestSitesPaging(t *testing.T) {
	c := newTestClient(t, gridHandler(101, 100, func(i int) string {
		return fmt.Sprintf(`{"siteId":"s%d","name":"n%d"}`, i, i)
	}))
	sites, err := c.Sites(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 101 {
		t.Fatalf("got %d sites, want 101", len(sites))
	}
}

func TestEnvelopeError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"errorCode":-7132,"msg":"busy"}`)
	})
	_, err := c.ControllerStatus(context.Background())
	var apiErr *cloud.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != -7132 {
		t.Fatalf("want APIError -7132, got %v", err)
	}
}

func TestNonJSONBody(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html>login</html>")
	})
	if _, err := c.Devices(context.Background(), "s1"); err == nil {
		t.Fatal("want error")
	}
}

func TestNon2xx(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := c.ControllerStatus(context.Background())
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("want error with 500, got %v", err)
	}
}

func TestRowLimit(t *testing.T) {
	c := newTestClient(t, gridHandler(6000, 100, func(i int) string {
		return fmt.Sprintf(`{"mac":"m%d"}`, i)
	}))
	cl, err := c.Clients(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cl) != 5000 {
		t.Fatalf("got %d clients, want 5000", len(cl))
	}
}

func TestFirmware(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "aa-bb") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"errorCode":0,"result":{"curFwVer":"1.0"}}`)
	})
	if _, err := c.Firmware(context.Background(), "s1", "aa-bb"); err != nil {
		t.Fatal(err)
	}
}

func TestSecretNotInErrors(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	c, err := New(config.Config{BaseURL: srv.URL, OmadacID: "o", ClientID: "c", ClientSecret: testSecret, InsecureTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ControllerStatus(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), testSecret) {
		t.Fatalf("secret leaked: %v", err)
	}
}

func TestListMethodsRejectMissingResult(t *testing.T) {
	ctx := context.Background()
	calls := map[string]func(c *Client) error{
		"sites":   func(c *Client) error { _, err := c.Sites(ctx); return err },
		"devices": func(c *Client) error { _, err := c.Devices(ctx, "s1"); return err },
		"ports":   func(c *Client) error { _, err := c.Ports(ctx, "s1"); return err },
		"clients": func(c *Client) error { _, err := c.Clients(ctx, "s1"); return err },
	}
	bodies := map[string]string{
		"empty object": `{}`,
		"null result":  `{"errorCode":0,"result":null}`,
	}
	for name, do := range calls {
		for bname, body := range bodies {
			t.Run(name+"/"+bname, func(t *testing.T) {
				c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
					fmt.Fprint(w, body)
				})
				if err := do(c); err == nil {
					t.Fatal("want error, got nil")
				}
			})
		}
	}
}

func TestEmptyResultIsValid(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"errorCode":0,"result":{"totalRows":0,"data":[]}}`)
	})
	got, err := c.Devices(context.Background(), "s1")
	if err != nil || len(got) != 0 {
		t.Fatalf("want empty and nil error, got %v, %v", got, err)
	}
}
