package rtorrent

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// value is one XML-RPC value as rTorrent answers it: a scalar, an array or a struct.
type value struct {
	text    string
	isInt   bool
	integer int64
	array   []value
	members map[string]value
}

// param is one call argument: a string, or raw bytes sent as base64.
type param struct {
	text string
	raw  []byte
}

func stringParam(text string) param { return param{text: text} }
func bytesParam(raw []byte) param   { return param{raw: raw} }

// encodeCall writes an XML-RPC methodCall.
func encodeCall(method string, params []param) []byte {
	var out bytes.Buffer
	out.WriteString(`<?xml version="1.0"?><methodCall><methodName>`)
	_ = xml.EscapeText(&out, []byte(method))
	out.WriteString(`</methodName><params>`)
	for _, argument := range params {
		out.WriteString(`<param><value>`)
		if argument.raw != nil {
			out.WriteString(`<base64>` + base64.StdEncoding.EncodeToString(argument.raw) + `</base64>`)
		} else {
			out.WriteString(`<string>`)
			_ = xml.EscapeText(&out, []byte(argument.text))
			out.WriteString(`</string>`)
		}
		out.WriteString(`</value></param>`)
	}
	out.WriteString(`</params></methodCall>`)
	return out.Bytes()
}

// FaultError is rTorrent refusing a call.
type FaultError struct {
	Code    int64
	Message string
}

func (e *FaultError) Error() string { return fmt.Sprintf("rtorrent: fault %d: %s", e.Code, e.Message) }

var errMalformed = errors.New("rtorrent: malformed XML-RPC response")

// decodeResponse reads a methodResponse's single value, or its fault.
func decodeResponse(body []byte) (value, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	fault := false
	for {
		token, err := decoder.Token()
		if err != nil {
			return value{}, errMalformed
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "fault":
			fault = true
		case "value":
			parsed, err := decodeValue(decoder)
			if err != nil {
				return value{}, err
			}
			if fault {
				return value{}, &FaultError{Code: parsed.members["faultCode"].integer, Message: parsed.members["faultString"].text}
			}
			return parsed, nil
		}
	}
}

// decodeValue reads the inside of one <value> up to its end; a bare value is a string.
func decodeValue(decoder *xml.Decoder) (value, error) {
	var parsed value
	typed := false
	for {
		token, err := decoder.Token()
		if err != nil {
			return value{}, errMalformed
		}
		switch element := token.(type) {
		case xml.CharData:
			if !typed {
				parsed.text += string(element)
			}
		case xml.EndElement:
			if element.Name.Local == "value" {
				return parsed, nil
			}
		case xml.StartElement:
			typed = true
			switch element.Name.Local {
			case "array":
				items, err := decodeArray(decoder)
				if err != nil {
					return value{}, err
				}
				parsed = value{array: items}
			case "struct":
				members, err := decodeStruct(decoder)
				if err != nil {
					return value{}, err
				}
				parsed = value{members: members}
			default:
				text, err := elementText(decoder)
				if err != nil {
					return value{}, err
				}
				parsed = scalar(element.Name.Local, text)
			}
		}
	}
}

func scalar(kind, text string) value {
	switch kind {
	case "i4", "i8", "int", "boolean":
		number, _ := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		return value{text: text, isInt: true, integer: number}
	case "base64":
		raw, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(text))
		return value{text: string(raw)}
	}
	return value{text: text}
}

func elementText(decoder *xml.Decoder) (string, error) {
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", errMalformed
		}
		switch element := token.(type) {
		case xml.CharData:
			text.Write(element)
		case xml.EndElement:
			return text.String(), nil
		}
	}
}

func decodeArray(decoder *xml.Decoder) ([]value, error) {
	var items []value
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, errMalformed
		}
		switch element := token.(type) {
		case xml.StartElement:
			if element.Name.Local == "value" {
				item, err := decodeValue(decoder)
				if err != nil {
					return nil, err
				}
				items = append(items, item)
			}
		case xml.EndElement:
			if element.Name.Local == "array" {
				return items, nil
			}
		}
	}
}

func decodeStruct(decoder *xml.Decoder) (map[string]value, error) {
	members := map[string]value{}
	name := ""
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, errMalformed
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch element.Name.Local {
			case "name":
				if name, err = elementText(decoder); err != nil {
					return nil, err
				}
			case "value":
				member, err := decodeValue(decoder)
				if err != nil {
					return nil, err
				}
				members[name] = member
			}
		case xml.EndElement:
			if element.Name.Local == "struct" {
				return members, nil
			}
		}
	}
}
