package vbox

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// soapClient speaks just enough SOAP to call VirtualBox's web service
// (vboxwebsrv): one request per method, every argument a plain string, and a
// single <returnval> or a fault back. Object references are opaque strings the
// service hands out and takes back.
type soapClient struct {
	endpoint string
	http     *http.Client
}

// soapArg is one named argument, in the order the method's WSDL declares.
type soapArg struct{ name, value string }

func arg(name string, value any) soapArg {
	switch v := value.(type) {
	case string:
		return soapArg{name, v}
	case int:
		return soapArg{name, strconv.Itoa(v)}
	case bool:
		return soapArg{name, strconv.FormatBool(v)}
	}
	panic(fmt.Sprintf("soap arg %s: unsupported type %T", name, value))
}

// transportError is a failure to reach the service at all, as opposed to a
// fault from a service that answered. Only the first is fixed by starting it.
type transportError struct{ err error }

func (e *transportError) Error() string { return e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

func isTransport(err error) bool {
	var te *transportError
	return errors.As(err, &te)
}

// call invokes method and returns its <returnval> text, empty for a method
// that returns nothing.
func (c *soapClient) call(method string, args ...soapArg) (string, error) {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:vbox="http://www.virtualbox.org/">` +
		`<soap:Body><vbox:` + method + `>`)
	for _, a := range args {
		b.WriteString("<" + a.name + ">")
		xml.EscapeText(&b, []byte(a.value))
		b.WriteString("</" + a.name + ">")
	}
	b.WriteString(`</vbox:` + method + `></soap:Body></soap:Envelope>`)

	req, err := http.NewRequest(http.MethodPost, c.endpoint, &b)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", `""`)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", &transportError{err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", &transportError{err}
	}

	ret, fault, err := parseSOAP(body)
	if err != nil {
		return "", fmt.Errorf("%s: HTTP %d, unreadable reply: %w", method, resp.StatusCode, err)
	}
	if fault != "" {
		return "", fmt.Errorf("%s: %s", method, fault)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: HTTP %d", method, resp.StatusCode)
	}
	return ret, nil
}

// parseSOAP pulls the returnval and any fault out of a reply, matching on
// local names because gSOAP picks its own namespace prefixes. A fault without
// a faultstring (vboxwebsrv's answer to a bad login) is reported by its code.
func parseSOAP(body []byte) (ret, fault string, err error) {
	d := xml.NewDecoder(bytes.NewReader(body))
	var in string
	var sawEnvelope, sawFault bool
	var faultString, faultCode string
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "Envelope":
				sawEnvelope = true
			case "Fault":
				sawFault = true
			case "returnval", "faultstring", "faultcode":
				in = t.Name.Local
			}
		case xml.EndElement:
			in = ""
		case xml.CharData:
			switch in {
			case "returnval":
				ret += string(t)
			case "faultstring":
				faultString += string(t)
			case "faultcode":
				faultCode += string(t)
			}
		}
	}
	if !sawEnvelope {
		return "", "", errors.New("not a SOAP envelope")
	}
	if sawFault {
		switch code := strings.TrimSpace(faultCode); {
		case strings.TrimSpace(faultString) != "":
			fault = strings.TrimSpace(faultString)
		case code != "":
			fault = "fault " + code
		default:
			fault = "unexplained fault"
		}
	}
	return strings.TrimSpace(ret), fault, nil
}
