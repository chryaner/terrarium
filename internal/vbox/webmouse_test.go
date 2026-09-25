package vbox

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeWebService answers the IMouse conversation the way vboxwebsrv does and
// records every call as "method arg=value ...", in order.
type fakeWebService struct {
	mu       sync.Mutex
	calls    []string
	faults   map[string]string // method -> faultstring
	returns  map[string]string // method -> returnval override
	password string
}

func newFakeWebService(t *testing.T) (*fakeWebService, *httptest.Server) {
	f := &fakeWebService{faults: map[string]string{}, returns: map[string]string{}, password: "pw"}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeWebService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	method, args := decodeCall(body)

	f.mu.Lock()
	var parts []string
	for _, a := range args {
		parts = append(parts, a.name+"="+a.value)
	}
	f.calls = append(f.calls, strings.TrimSpace(method+" "+strings.Join(parts, " ")))
	fault, faulted := f.faults[method]
	ret, overridden := f.returns[method]
	f.mu.Unlock()

	if method == "IWebsessionManager_logon" && args[1].value != f.password {
		fault, faulted = "Invalid username or password", true
	}
	if faulted {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/"><SOAP-ENV:Body><SOAP-ENV:Fault><faultcode>SOAP-ENV:Client</faultcode><faultstring>%s</faultstring></SOAP-ENV:Fault></SOAP-ENV:Body></SOAP-ENV:Envelope>`, fault)
		return
	}
	if !overridden {
		ret = map[string]string{
			"IWebsessionManager_logon":            "vbox-ref",
			"IVirtualBox_findMachine":             "machine-ref",
			"IWebsessionManager_getSessionObject": "session-ref",
			"ISession_getConsole":                 "console-ref",
			"IConsole_getMouse":                   "mouse-ref",
			"IMouse_getAbsoluteSupported":         "true",
		}[method]
	}
	fmt.Fprintf(w, `<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/" xmlns:vbox="http://www.virtualbox.org/"><SOAP-ENV:Body><vbox:%sResponse><returnval>%s</returnval></vbox:%sResponse></SOAP-ENV:Body></SOAP-ENV:Envelope>`, method, ret, method)
}

// decodeCall reads the method element and its argument children back out of
// a request, so the tests check what actually went over the wire.
func decodeCall(body []byte) (string, []soapArg) {
	d := xml.NewDecoder(strings.NewReader(string(body)))
	var method string
	var args []soapArg
	depth := 0
	var cur *soapArg
	for {
		tok, err := d.Token()
		if err != nil {
			return method, args
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch depth {
			case 3: // Envelope > Body > method
				method = t.Name.Local
			case 4:
				args = append(args, soapArg{name: t.Name.Local})
				cur = &args[len(args)-1]
			}
		case xml.CharData:
			if cur != nil {
				cur.value += string(t)
			}
		case xml.EndElement:
			if depth == 4 {
				cur = nil
			}
			depth--
		}
	}
}

func (f *fakeWebService) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, strings.Fields(c)[0])
	}
	return out
}

func (f *fakeWebService) callsTo(method string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if strings.Fields(c)[0] == method {
			out = append(out, c)
		}
	}
	return out
}

func testWebMouse(endpoint string) *webMouse {
	return &webMouse{
		soap:     &soapClient{endpoint: endpoint, http: &http.Client{Timeout: 5 * time.Second}},
		user:     webUser,
		password: "pw",
		ensure:   func() error { return errors.New("ensure should not be called") },
	}
}

func TestWebMouseClickConversation(t *testing.T) {
	f, srv := newFakeWebService(t)
	w := testWebMouse(srv.URL)

	err := w.putEvents("trr-desk", []MouseEvent{{X: 10, Y: 20, Buttons: MouseLeft}, {X: 10, Y: 20}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"IWebsessionManager_logon",
		"IVirtualBox_findMachine",
		"IWebsessionManager_getSessionObject",
		"IMachine_lockMachine",
		"ISession_getConsole",
		"IConsole_getMouse",
		"IMouse_getAbsoluteSupported",
		"IMouse_putMouseEventAbsolute",
		"IMouse_putMouseEventAbsolute",
		"ISession_unlockMachine",
		"IWebsessionManager_logoff",
	}
	if got := f.methods(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("calls:\n got %v\nwant %v", got, want)
	}
	checks := map[string]string{
		"IWebsessionManager_logon":     "IWebsessionManager_logon username=terrarium password=pw",
		"IVirtualBox_findMachine":      "IVirtualBox_findMachine _this=vbox-ref nameOrId=trr-desk",
		"IMachine_lockMachine":         "IMachine_lockMachine _this=machine-ref session=session-ref lockType=Shared",
		"IMouse_putMouseEventAbsolute": "IMouse_putMouseEventAbsolute _this=mouse-ref x=11 y=21 dz=0 dw=0 buttonState=1",
		"IWebsessionManager_logoff":    "IWebsessionManager_logoff refIVirtualBox=vbox-ref",
	}
	for method, wantCall := range checks {
		if got := f.callsTo(method)[0]; got != wantCall {
			t.Errorf("%s:\n got %q\nwant %q", method, got, wantCall)
		}
	}
	if release := f.callsTo("IMouse_putMouseEventAbsolute")[1]; !strings.HasSuffix(release, "buttonState=0") {
		t.Errorf("second event should release the button: %q", release)
	}
}

func TestWebMouseWheelDoesNotMovePointer(t *testing.T) {
	f, srv := newFakeWebService(t)
	if err := testWebMouse(srv.URL).wheel("trr-desk", -3); err != nil {
		t.Fatal(err)
	}
	got := f.callsTo("IMouse_putMouseEvent")
	if len(got) != 1 || got[0] != "IMouse_putMouseEvent _this=mouse-ref dx=0 dy=0 dz=-3 dw=0 buttonState=0" {
		t.Errorf("wheel call: %v", got)
	}
	if n := len(f.callsTo("IMouse_putMouseEventAbsolute")); n != 0 {
		t.Errorf("wheel sent %d absolute events", n)
	}
}

func TestWebMouseNoTablet(t *testing.T) {
	f, srv := newFakeWebService(t)
	f.returns["IMouse_getAbsoluteSupported"] = "false"
	err := testWebMouse(srv.URL).putEvents("trr-old", []MouseEvent{{X: 1, Y: 1}}, 0)
	if err == nil || !strings.Contains(err.Error(), "--mouse usbtablet") {
		t.Fatalf("want the usbtablet fix, got %v", err)
	}
	if n := len(f.callsTo("IMouse_putMouseEventAbsolute")); n != 0 {
		t.Errorf("sent %d events to a VM without a tablet", n)
	}
	if len(f.callsTo("IWebsessionManager_logoff")) != 1 {
		t.Error("should still log off")
	}
}

// A VM that is not running refuses the lock; the error has to say so, and the
// session must still be cleaned up.
func TestWebMouseLockFaultCleansUp(t *testing.T) {
	f, srv := newFakeWebService(t)
	f.faults["IMachine_lockMachine"] = "The machine is not mutable or running"
	err := testWebMouse(srv.URL).putEvents("trr-off", []MouseEvent{{X: 1, Y: 1}}, 0)
	if err == nil || !strings.Contains(err.Error(), "is it running?") || !strings.Contains(err.Error(), "not mutable or running") {
		t.Fatalf("want the fault and a hint, got %v", err)
	}
	if len(f.callsTo("ISession_unlockMachine")) != 0 {
		t.Error("unlocked a session that never locked")
	}
	if len(f.callsTo("IWebsessionManager_logoff")) != 1 {
		t.Error("should log off after a failed lock")
	}
}

func TestWebMouseBadPassword(t *testing.T) {
	_, srv := newFakeWebService(t)
	w := testWebMouse(srv.URL)
	w.password = "wrong"
	err := w.putEvents("trr-desk", []MouseEvent{{X: 1, Y: 1}}, 0)
	if err == nil || !strings.Contains(err.Error(), "Invalid username or password") || !strings.Contains(err.Error(), "pkill vboxwebsrv") {
		t.Fatalf("want the auth fault and the fix, got %v", err)
	}
}

// Nothing listening means the service is down: start it once, then carry on.
func TestWebMouseStartsServiceWhenUnreachable(t *testing.T) {
	f := &fakeWebService{faults: map[string]string{}, returns: map[string]string{}, password: "pw"}
	srv := httptest.NewUnstartedServer(f)
	t.Cleanup(srv.Close)
	endpoint := "http://" + srv.Listener.Addr().String()

	w := testWebMouse(endpoint)
	starts := 0
	w.ensure = func() error {
		starts++
		srv.Start()
		return nil
	}
	if err := w.putEvents("trr-desk", []MouseEvent{{X: 1, Y: 1}}, 0); err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Errorf("ensure called %d times, want 1", starts)
	}
	if n := len(f.callsTo("IMouse_putMouseEventAbsolute")); n != 1 {
		t.Errorf("sent %d events after starting, want 1", n)
	}
}

func TestWebMouseStartFailureIsReported(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	endpoint := "http://" + srv.Listener.Addr().String()
	srv.Listener.Close()

	w := testWebMouse(endpoint)
	w.ensure = func() error { return errors.New("vboxwebsrv not found") }
	err := w.putEvents("trr-desk", []MouseEvent{{X: 1, Y: 1}}, 0)
	if err == nil || !strings.Contains(err.Error(), "vboxwebsrv not found") {
		t.Fatalf("want the start failure, got %v", err)
	}
}

func TestParseSOAP(t *testing.T) {
	cases := []struct {
		name, body, ret, fault string
		bad                    bool
	}{
		{"returnval with a gSOAP prefix", `<SOAP-ENV:Envelope xmlns:SOAP-ENV="s" xmlns:vbox="v"><SOAP-ENV:Body><vbox:xResponse><returnval>abc-123</returnval></vbox:xResponse></SOAP-ENV:Body></SOAP-ENV:Envelope>`, "abc-123", "", false},
		{"void method", `<e:Envelope xmlns:e="s"><e:Body><v:xResponse xmlns:v="v"/></e:Body></e:Envelope>`, "", "", false},
		{"fault", `<e:Envelope xmlns:e="s"><e:Body><e:Fault><faultstring>VirtualBox error: nope</faultstring></e:Fault></e:Body></e:Envelope>`, "", "VirtualBox error: nope", false},
		{"escaped text", `<e:Envelope xmlns:e="s"><e:Body><returnval>a &amp; b</returnval></e:Body></e:Envelope>`, "a & b", "", false},
		{"fault with only a code", `<e:Envelope xmlns:e="s"><e:Body><e:Fault><faultcode>SOAP-ENV:Client</faultcode></e:Fault></e:Body></e:Envelope>`, "", "fault SOAP-ENV:Client", false},
		{"empty fault", `<e:Envelope xmlns:e="s"><e:Body><e:Fault></e:Fault></e:Body></e:Envelope>`, "", "unexplained fault", false},
		{"not soap", `<html>502 Bad Gateway</html>`, "", "", true},
		{"garbage", `not xml at all <`, "", "", true},
	}
	for _, c := range cases {
		ret, fault, err := parseSOAP([]byte(c.body))
		if (err != nil) != c.bad {
			t.Errorf("%s: err = %v", c.name, err)
			continue
		}
		if ret != c.ret || fault != c.fault {
			t.Errorf("%s: got ret %q fault %q, want %q %q", c.name, ret, fault, c.ret, c.fault)
		}
	}
}

// Argument text goes over the wire escaped, so a VM name with XML
// metacharacters cannot break the request.
func TestSOAPEscapesArguments(t *testing.T) {
	f, srv := newFakeWebService(t)
	c := &soapClient{endpoint: srv.URL, http: &http.Client{Timeout: 5 * time.Second}}
	if _, err := c.call("IVirtualBox_findMachine", arg("_this", "v"), arg("nameOrId", `a<b>&"c"`)); err != nil {
		t.Fatal(err)
	}
	if got := f.callsTo("IVirtualBox_findMachine")[0]; !strings.HasSuffix(got, `nameOrId=a<b>&"c"`) {
		t.Errorf("round trip lost the name: %q", got)
	}
}

func TestSOAPTransportErrorIsDistinct(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	endpoint := "http://" + srv.Listener.Addr().String()
	srv.Listener.Close()
	c := &soapClient{endpoint: endpoint, http: &http.Client{Timeout: 2 * time.Second}}
	_, err := c.call("IWebsessionManager_logon")
	if !isTransport(err) {
		t.Errorf("a refused connection should be a transport error, got %T %v", err, err)
	}

	f, up := newFakeWebService(t)
	f.faults["IWebsessionManager_logon"] = "boom"
	c.endpoint = up.URL
	if _, err := c.call("IWebsessionManager_logon", arg("username", "u"), arg("password", "pw")); err == nil || isTransport(err) {
		t.Errorf("a fault is not a transport error, got %v", err)
	}
}

func TestAuthSimpleHash(t *testing.T) {
	// sha256("secret"), as VBoxAuthSimple compares it.
	if got := authSimpleHash("secret"); got != "2bb80d537b1da3e38bd30361aa855686bde0eacd7162fef6a25fe97bf527a25b" {
		t.Errorf("got %s", got)
	}
}

func TestLoadOrCreateSecret(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "terrarium")
	first, err := loadOrCreateSecret(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 32 {
		t.Errorf("secret %q should be 32 hex chars", first)
	}
	again, err := loadOrCreateSecret(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Error("the secret must be stable across calls, or a running service would reject us")
	}
	if fi, err := os.Stat(filepath.Join(dir, secretFile)); err != nil {
		t.Fatal(err)
	} else if mode := fi.Mode().Perm(); os.PathSeparator == '/' && mode != 0o600 {
		t.Errorf("secret file mode %v, want 0600", mode)
	}
}
