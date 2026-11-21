package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"strconv"
	"strings"
)

// EncodeMultipart keeps upstream framing in the shared task codec. File
// references must be resolved by a durable file host, never interpreted as a
// local path or fetched using arbitrary upstream credentials.
func EncodeMultipart(parts []Part) ([]byte, string, error) {
	if len(parts) > 64 {
		return nil, "", errors.New("too many multipart fields")
	}
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for _, part := range parts {
		if part.FileRef != "" {
			return nil, "", errors.New("multipart file references require the file host service")
		}
		if part.Name == "" || len(part.Name) > 128 || strings.ContainsAny(part.Name, "\r\n\x00") || len(part.Value) > MaxSnapshotBytes {
			return nil, "", errors.New("invalid multipart field")
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(part.Value))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			return nil, "", errors.New("invalid multipart field value")
		}
		var text string
		switch typed := value.(type) {
		case string:
			text = typed
		case json.Number:
			text = typed.String()
		case bool:
			text = strconv.FormatBool(typed)
		case nil:
			text = ""
		default:
			return nil, "", errors.New("multipart text fields must contain a scalar value")
		}
		if err := writer.WriteField(part.Name, text); err != nil {
			return nil, "", err
		}
		if buffer.Len() > MaxSnapshotBytes {
			return nil, "", errors.New("multipart request exceeds size limit")
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	if buffer.Len() > MaxSnapshotBytes {
		return nil, "", errors.New("multipart request exceeds size limit")
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
}
