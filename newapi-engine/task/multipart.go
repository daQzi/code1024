package task

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
)

// EncodeMultipart keeps upstream framing in the shared task codec. File
// references must be resolved by a durable file host, never interpreted as a
// local path or fetched using arbitrary upstream credentials.
func EncodeMultipart(parts []Part) ([]byte, string, error) {
	var buffer bytes.Buffer
	contentType, err := WriteMultipart(context.Background(), &buffer, parts, nil)
	if err != nil {
		return nil, "", err
	}
	if buffer.Len() > MaxSnapshotBytes {
		return nil, "", errors.New("multipart request exceeds size limit")
	}
	return buffer.Bytes(), contentType, nil
}

func multipartScalar(raw json.RawMessage) (string, error) {
	if len(raw) > MaxSnapshotBytes || !json.Valid(raw) {
		return "", errors.New("invalid multipart field value")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	switch value := value.(type) {
	case string:
		return value, nil
	case json.Number:
		return value.String(), nil
	case bool:
		return strconv.FormatBool(value), nil
	case nil:
		return "", nil
	default:
		return "", errors.New("multipart text fields must contain a scalar value")
	}
}
