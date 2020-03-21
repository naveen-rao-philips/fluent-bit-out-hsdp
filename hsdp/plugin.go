package hsdp

import (
	"C"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unsafe"

	"github.com/fluent/fluent-bit-go/output"
	"github.com/m4rw3r/uuid"
	"github.com/philips-software/go-hsdp-api/logging"
)

var (
	// both variables are set in Makefile
	revision  string
	builddate string
	plugin    Plugin = &fluentPlugin{}
	client    *logging.Client
)

type fluentPlugin struct{}

// Plugin interface for verifying all methods are available
type Plugin interface {
	Environment(ctx unsafe.Pointer, key string) string
	Unregister(ctx unsafe.Pointer)
	GetRecord(dec *output.FLBDecoder) (ret int, ts interface{}, rec map[interface{}]interface{})
	NewDecoder(data unsafe.Pointer, length int) *output.FLBDecoder
	Send(values []logging.Resource) error
	Exit(code int)
}

func (p *fluentPlugin) Environment(ctx unsafe.Pointer, key string) string {
	// Environment variables have priority
	envKey := "HSDP_" + strings.ToUpper(CamelCaseToUnderscore(key))
	if value := os.Getenv(envKey); value != "" {
		return value
	}
	return output.FLBPluginConfigKey(ctx, key)
}

func (p *fluentPlugin) Unregister(ctx unsafe.Pointer) {
	output.FLBPluginUnregister(ctx)
}

func (p *fluentPlugin) GetRecord(dec *output.FLBDecoder) (int, interface{}, map[interface{}]interface{}) {
	return output.GetRecord(dec)
}

func (p *fluentPlugin) NewDecoder(data unsafe.Pointer, length int) *output.FLBDecoder {
	return output.NewDecoder(data, int(length))
}

func (p *fluentPlugin) Exit(code int) {
	os.Exit(code)
}

func (p *fluentPlugin) Send(values []logging.Resource) error {
	// TODO
	return nil
}

//export FLBPluginRegister
func FLBPluginRegister(ctx unsafe.Pointer) int {
	return output.FLBPluginRegister(ctx, "hsdp", "HSDP logging output plugin")
}

//export FLBPluginInit
func FLBPluginInit(ctx unsafe.Pointer) int {
	host := plugin.Environment(ctx, "IngestorHost")
	sharedKey := plugin.Environment(ctx, "SharedKey")
	secretKey := plugin.Environment(ctx, "SecretKey")
	productKey := plugin.Environment(ctx, "ProductKey")
	var err error

	client, err = logging.NewClient(nil,
		logging.Config{
			SharedKey:    sharedKey,
			SharedSecret: secretKey,
			ProductKey:   productKey,
			BaseURL:      host,
		})
	if err != nil {
		fmt.Printf("configuration errors: %v\n", err)
		plugin.Unregister(ctx)
		plugin.Exit(1)
		return output.FLB_ERROR
	}
	fmt.Printf("[out-hsdp] build:%s version:%s\n", builddate, revision)
	return output.FLB_OK
}

//export FLBPluginFlush
func FLBPluginFlush(data unsafe.Pointer, length C.int, tag *C.char) int {
	// do something with the data
	var ret int
	var ts interface{}
	var record map[interface{}]interface{}

	var resources []logging.Resource

	// Create Fluent Bit decoder
	dec := plugin.NewDecoder(data, int(length))

	// TODO trigger store at 25 records
	for {
		// Extract Record
		ret, ts, record = plugin.GetRecord(dec)
		if ret != 0 {
			break
		}

		// Print record keys and values
		var timeStamp time.Time
		switch t := ts.(type) {
		case output.FLBTime:
			timeStamp = ts.(output.FLBTime).Time
		case uint64:
			timeStamp = time.Unix(int64(t), 0)
		default:
			fmt.Print("given time is not in a known format, defaulting to now.\n")
			timeStamp = time.Now()
		}

		js, err := createResource(timeStamp, C.GoString(tag), record)
		if err != nil {
			fmt.Printf("%v\n", err)
			// DO NOT RETURN HERE becase one message has an error when json is
			// generated, but a retry would fetch ALL messages again. instead an
			// error should be printed to console
			continue
		}
		resources = append(resources, *js)
	}
	fmt.Printf("[out-hsdp] flushing %d resources\n", len(resources))

	_, err := client.StoreResources(resources, len(resources))

	if err != nil {
		fmt.Printf("[out-hsdp] error: %v\n", err)
		return output.FLB_ERROR
	}
	return output.FLB_OK
}

func mapReturnDelete(m *map[string]interface{}, key, defaultValue string) string {
	output := defaultValue
	if val, ok := (*m)[key].(string); ok && val != "" {
		output = val
		delete(*m, key)
	}
	return output
}

type jsonArray []interface{}

func (a jsonArray) Value() ([]byte, error) {
	return json.Marshal(a)
}

type jsonMap map[string]interface{}

func (m jsonMap) Value() ([]byte, error) {
	return json.Marshal(m)
}

func recursiveToJSON(v interface{}) (r interface{}) {
	switch v := v.(type) {
	case []interface{}:
		for i, e := range v {
			v[i] = recursiveToJSON(e)
		}
		r = jsonArray(v)
	case map[interface{}]interface{}:
		newMap := make(map[string]interface{}, len(v))
		for k, e := range v {
			newMap[k.(string)] = recursiveToJSON(e)
		}
		r = jsonMap(newMap)
	case []byte:
		// Prevent base64 encoding
		r = string(v)
	default:
		r = v
	}
	return
}

func createResource(timestamp time.Time, tag string, record map[interface{}]interface{}) (*logging.Resource, error) {
	m := make(map[string]interface{})
	// convert timestamp to RFC3339Nano which is logstash format
	m["@timestamp"] = timestamp.UTC().Format(time.RFC3339Nano)
	m["@tag"] = tag
	for k, v := range record {
		switch t := v.(type) {
		case []byte:
			// prevent encoding to base64
			m[k.(string)] = string(t)
		case string:
			m[k.(string)] = v
		case []interface{}, map[interface{}]interface{}:
			m[k.(string)] = recursiveToJSON(v)
		default:
			fmt.Printf("[out-hsdp] skipping type %v for now\n", t)
			//m[k.(string)] = v
		}
	}
	id, _ := uuid.V4()
	transactionID, _ := uuid.V4()

	serverName := mapReturnDelete(&m, "server_name", "fluent-bit")
	appInstance := mapReturnDelete(&m, "app_instance", "fluent-bit")
	appName := mapReturnDelete(&m, "app_name", "fluent-bit")
	appVersion := mapReturnDelete(&m, "app_version", "1.0")
	component := mapReturnDelete(&m, "component", "fluent-bit")
	severity := mapReturnDelete(&m, "severity", "Informational")
	category := mapReturnDelete(&m, "category", "Tracelog")
	serviceName := mapReturnDelete(&m, "service_name", "fluent-bit")
	originatingUser := mapReturnDelete(&m, "originating_user", "fluent-bit")
	eventID := mapReturnDelete(&m, "event_id", "1")

	msg, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("error creating message for hsdp-logging: %v", err)
	}
	return &logging.Resource{
		ID:                  id.String(),
		Severity:            severity,
		ApplicationInstance: appInstance,
		ApplicationName:     appName,
		OriginatingUser:     originatingUser,
		Category:            category,
		Component:           component,
		ApplicationVersion:  appVersion,
		ServerName:          serverName,
		ServiceName:         serviceName,
		EventID:             eventID,
		TransactionID:       transactionID.String(),
		LogTime:             timestamp.UTC().Format(logging.LogTimeFormat),
		LogData:             logging.LogData{Message: string(msg)},
	}, nil
}

//export FLBPluginExit
func FLBPluginExit() int {
	return output.FLB_OK
}