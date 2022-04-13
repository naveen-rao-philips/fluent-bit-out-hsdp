package hsdp

import (
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/influxdata/go-syslog/v2/rfc5424"
	"github.com/philips-software/go-hsdp-api/logging"
)

type logDrainerStorer struct {
	*http.Client
	logDrainerURL *url.URL
}

func (l *logDrainerStorer) StoreResources(messages []logging.Resource, count int) (*logging.StoreResponse, error) {
	var errs []error
	var resp *http.Response
	logResponse := &logging.StoreResponse{}

	for i := 0; i < count; i++ {
		var err error
		msg := messages[i]
		decoded, err := base64.StdEncoding.DecodeString(msg.LogData.Message)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		syslogMessage := rfc5424.SyslogMessage{}
		syslogMessage.SetTimestamp(time.Now().Format(time.RFC3339))
		syslogMessage.SetPriority(14)
		syslogMessage.SetVersion(1)
		syslogMessage.SetProcID("[APP/PROC/FLUENT-BIT-OUT-HSDP/0]")
		syslogMessage.SetAppname(msg.ApplicationName)
		syslogMessage.SetHostname(msg.ServerName)
		syslogMessage.SetParameter("fluent-bit-out-hsdp", "taskId", msg.ApplicationInstance)
		syslogMessage.SetMessage(string(decoded))
		message, _ := syslogMessage.String()
		req := &http.Request{
			Method: http.MethodPost,
			URL:    l.logDrainerURL,
			Header: http.Header{
				"Content-Type": []string{"text/plain"},
			},
		}
		req.Body = ioutil.NopCloser(strings.NewReader(message))
		resp, err = l.Client.Do(req)
		if err != nil {
			errs = append(errs, err)
		}
		if resp == nil || resp.StatusCode != http.StatusOK {
			_, _ = fmt.Fprintf(os.Stderr, "failed to send log: %v %v", resp, err)
		}
	}
	logResponse.Response = &http.Response{StatusCode: http.StatusOK}
	return logResponse, nil
}

func NewLogDrainerStorer(logDrainerURL string) (storer, error) {
	if logDrainerURL == "" {
		return nil, fmt.Errorf("missing or empty logDrainerURL")
	}
	parsedURL, err := url.Parse(logDrainerURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL '%s': %v", logDrainerURL, err)
	}
	storer := &logDrainerStorer{
		Client:        &http.Client{},
		logDrainerURL: parsedURL,
	}

	return storer, nil
}
