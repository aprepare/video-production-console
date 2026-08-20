package partnergateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminRoutesHiddenWhenPasswordMissing(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(upstream.Close)
	fixture := newHTTPTestEnvironment(t, upstream.URL, "upstream-key", nil)

	w := httptest.NewRecorder()
	fixture.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin", nil))
	assertErrorResponse(t, w, http.StatusNotFound, "not_found")
}

func TestAdminLoginListsPartnersWithoutActivationKey(t *testing.T) {
	handler, cookie := newAdminFixture(t)

	list := performAdminJSON(t, handler, http.MethodGet, "/admin/api/partners", "", cookie)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), `"activation_key"`) {
		t.Fatalf("list leaked activation key: %s", list.Body.String())
	}
	var payload struct {
		Partners []adminPartnerView `json:"partners"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Partners) != 1 || payload.Partners[0].DisplayName != "fixture partner" {
		t.Fatalf("partners=%+v", payload.Partners)
	}
}

func TestAdminCreateReturnsKeyOnceAndListDoesNot(t *testing.T) {
	handler, cookie := newAdminFixture(t)

	created := performAdminJSON(t, handler, http.MethodPost, "/admin/api/partners", `{"display_name":"居中观"}`, cookie)
	if created.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var createdBody struct {
		ID            string `json:"id"`
		DisplayName   string `json:"display_name"`
		ActivationKey string `json:"activation_key"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil {
		t.Fatal(err)
	}
	if createdBody.ID == "" || createdBody.DisplayName != "居中观" || !strings.HasPrefix(createdBody.ActivationKey, "vpc_") {
		t.Fatalf("created=%+v", createdBody)
	}

	listed := performAdminJSON(t, handler, http.MethodGet, "/admin/api/partners", "", cookie)
	if strings.Contains(listed.Body.String(), createdBody.ActivationKey) {
		t.Fatalf("list echoed activation key")
	}
}

func TestAdminDisableAndUnbindRequireSession(t *testing.T) {
	handler, _ := newAdminFixture(t)
	w := performAdminJSON(t, handler, http.MethodPost, "/admin/api/partners/disable", `{"id":"missing"}`, "")
	assertErrorResponse(t, w, http.StatusUnauthorized, "authorization_failed")
}

func TestAdminWrongPasswordDoesNotCreateSession(t *testing.T) {
	handler, _ := newAdminFixture(t)
	w := performAdminJSON(t, handler, http.MethodPost, "/admin/api/login", `{"password":"wrong-password"}`, "")
	assertErrorResponse(t, w, http.StatusUnauthorized, "authorization_failed")
	if cookie := w.Result().Cookies(); len(cookie) != 0 {
		t.Fatalf("unexpected cookies=%v", cookie)
	}
}

func newAdminFixture(t *testing.T) (http.Handler, string) {
	t.Helper()
	upstream := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(upstream.Close)
	fixture := newHTTPTestEnvironment(t, upstream.URL, "upstream-key", func(options *ServerOptions) {
		options.AdminPassword = "admin-test-password"
	})
	page := httptest.NewRecorder()
	fixture.handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/admin", nil))
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "伙伴激活台账") {
		t.Fatalf("admin page status=%d", page.Code)
	}
	login := performAdminJSON(t, fixture.handler, http.MethodPost, "/admin/api/login", `{"password":"admin-test-password"}`, "")
	if login.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", login.Code, login.Body.String())
	}
	cookie := ""
	for _, item := range login.Result().Cookies() {
		if item.Name == adminCookieName {
			cookie = item.Value
		}
	}
	if cookie == "" {
		t.Fatal("admin cookie missing")
	}
	return fixture.handler, cookie
}

func performAdminJSON(t *testing.T, handler http.Handler, method, target, body, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		request.AddCookie(&http.Cookie{Name: adminCookieName, Value: cookie})
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	return w
}
