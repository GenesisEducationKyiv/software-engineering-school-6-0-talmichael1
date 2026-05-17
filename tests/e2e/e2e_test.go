//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
)

var (
	pw     *playwright.Playwright
	expect playwright.PlaywrightAssertions
)

func TestMain(m *testing.M) {
	if err := playwright.Install(&playwright.RunOptions{Browsers: []string{"chromium"}}); err != nil {
		fmt.Fprintln(os.Stderr, "installing playwright driver:", err)
		os.Exit(1)
	}
	var err error
	pw, err = playwright.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "starting playwright:", err)
		os.Exit(1)
	}
	expect = playwright.NewPlaywrightAssertions(15000)

	code := m.Run()
	_ = pw.Stop()
	os.Exit(code)
}

func baseURL() string {
	if v := os.Getenv("E2E_BASE_URL"); v != "" {
		return v
	}
	return "http://localhost:3000"
}

func newPage(t *testing.T) playwright.Page {
	t.Helper()
	launchOpts := playwright.BrowserTypeLaunchOptions{}
	if v := os.Getenv("E2E_HOST_RESOLVER_RULES"); v != "" {
		launchOpts.Args = []string{"--host-resolver-rules=" + v}
	}
	browser, err := pw.Chromium.Launch(launchOpts)
	if err != nil {
		t.Fatalf("launching browser: %v", err)
	}
	t.Cleanup(func() { _ = browser.Close() })
	ctx, err := browser.NewContext()
	if err != nil {
		t.Fatalf("creating browser context: %v", err)
	}
	page, err := ctx.NewPage()
	if err != nil {
		t.Fatalf("opening page: %v", err)
	}
	return page
}

func uniqueEmail(prefix string) string {
	return fmt.Sprintf("%s-%d@example.com", prefix, time.Now().UnixNano())
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSubscribeShowsSuccessMessage(t *testing.T) {
	page := newPage(t)
	if _, err := page.Goto(baseURL()); err != nil {
		t.Fatalf("navigating to %s: %v", baseURL(), err)
	}

	email := uniqueEmail("e2e-subscribe")
	must(t, page.Locator("#email").Fill(email))
	must(t, page.Locator("#repo").Fill("golang/go"))
	must(t, page.Locator("button", playwright.PageLocatorOptions{HasText: "Subscribe"}).First().Click())

	alert := page.Locator(".alert-success")
	must(t, expect.Locator(alert).ToBeVisible())
	must(t, expect.Locator(alert).ToContainText("Subscription successful"))
}

func TestViewListShowsPendingSubscription(t *testing.T) {
	page := newPage(t)
	if _, err := page.Goto(baseURL()); err != nil {
		t.Fatalf("navigating to %s: %v", baseURL(), err)
	}

	email := uniqueEmail("e2e-view")
	must(t, page.Locator("#email").Fill(email))
	must(t, page.Locator("#repo").Fill("golang/go"))
	must(t, page.Locator("button", playwright.PageLocatorOptions{HasText: "Subscribe"}).First().Click())
	must(t, expect.Locator(page.Locator(".alert-success")).ToBeVisible())

	must(t, page.Locator(`input[placeholder="Enter email to view subscriptions"]`).Fill(email))
	must(t, page.Locator("button", playwright.PageLocatorOptions{HasText: "View"}).First().Click())

	row := page.Locator(".sub-item", playwright.PageLocatorOptions{HasText: "golang/go"})
	must(t, expect.Locator(row).ToBeVisible())
	must(t, expect.Locator(row.Locator(".badge-pending")).ToHaveText("Pending"))
}
