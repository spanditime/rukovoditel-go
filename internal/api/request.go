package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Request describes a low-level Rukovoditel action.
type Request struct {
	Action   string
	EntityID int64
	Params   map[string]any
}

// Do sends an action and returns its raw response envelope.
func (c *Client) Do(
	ctx context.Context,
	req Request,
) (Response, error) {
	if strings.TrimSpace(req.Action) == "" {
		return Response{}, fmt.Errorf("api: action is required")
	}

	params := make(map[string]any, len(req.Params)+5)

	for k, v := range req.Params {
		if k == "key" || k == "username" || k == "password" ||
			k == "action" || k == "entity_id" {
			return Response{}, fmt.Errorf(
				"api: reserved parameter %q",
				k,
			)
		}
		params[k] = v
	}

	params["key"] = c.key
	params["username"] = c.username
	params["password"] = c.password
	params["action"] = req.Action

	if req.EntityID != 0 {
		params["entity_id"] = req.EntityID
	}

	var (
		body        []byte
		contentType string
		err         error
	)

	switch c.encoding {
	case JSONEncoding:
		body, err = json.Marshal(params)
		contentType = "application/json"

	case FormEncoding:
		var values url.Values
		values, err = encodeForm(params)
		if err == nil {
			body = []byte(values.Encode())
		}
		contentType = "application/x-www-form-urlencoded"
	}

	if err != nil {
		return Response{}, c.safeError("encode request", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return Response{}, c.safeError("create request", err)
	}

	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return Response{}, c.safeError("send request", err)
	}
	defer httpResp.Body.Close()

	const maxResponseSize = 32 << 20 // 32 MiB

	responseBody, err := io.ReadAll(
		io.LimitReader(httpResp.Body, maxResponseSize+1),
	)
	if err != nil {
		return Response{}, c.safeError("read response", err)
	}
	if len(responseBody) > maxResponseSize {
		return Response{}, fmt.Errorf("api: response exceeds size limit")
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		return Response{}, &HTTPError{StatusCode: httpResp.StatusCode, Body: responseBody}
	}

	var result Response
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return Response{}, c.safeError("decode response", err)
	}

	if result.Status != "success" {
		return result, &APIError{
			Status: result.Status,
			Data:   result.Data,
			Body:   responseBody,
		}
	}

	return result, nil
}

func encodeForm(params map[string]any) (url.Values, error) {
	values := make(url.Values)

	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		if err := addFormValue(values, key, reflect.ValueOf(params[key])); err != nil {
			return nil, err
		}
	}

	return values, nil
}

func addFormValue(
	values url.Values,
	key string,
	value reflect.Value,
) error {
	if !value.IsValid() {
		values.Add(key, "")
		return nil
	}

	for value.Kind() == reflect.Interface ||
		value.Kind() == reflect.Pointer {
		if value.IsNil() {
			values.Add(key, "")
			return nil
		}
		value = value.Elem()
	}

	switch value.Kind() {
	case reflect.Map:
		if value.IsNil() {
			return nil
		}

		keys := value.MapKeys()
		sort.Slice(keys, func(i, j int) bool {
			return fmt.Sprint(keys[i].Interface()) <
				fmt.Sprint(keys[j].Interface())
		})

		for _, mapKey := range keys {
			childKey := key + "[" +
				fmt.Sprint(mapKey.Interface()) + "]"

			if err := addFormValue(
				values, childKey, value.MapIndex(mapKey),
			); err != nil {
				return err
			}
		}

	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			childKey := key + "[" + strconv.Itoa(i) + "]"
			if err := addFormValue(
				values, childKey, value.Index(i),
			); err != nil {
				return err
			}
		}

	case reflect.String:
		values.Add(key, value.String())

	case reflect.Bool:
		values.Add(key, strconv.FormatBool(value.Bool()))

	case reflect.Int, reflect.Int8, reflect.Int16,
		reflect.Int32, reflect.Int64:
		values.Add(key, strconv.FormatInt(value.Int(), 10))

	case reflect.Uint, reflect.Uint8, reflect.Uint16,
		reflect.Uint32, reflect.Uint64:
		values.Add(key, strconv.FormatUint(value.Uint(), 10))

	case reflect.Float32, reflect.Float64:
		values.Add(key, strconv.FormatFloat(
			value.Float(), 'f', -1, value.Type().Bits(),
		))

	default:
		return fmt.Errorf(
			"api: unsupported form value type %s for %q",
			value.Type(), key,
		)
	}

	return nil
}
