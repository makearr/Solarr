package extract

import (
	"bytes"
	"encoding/xml"
	"strings"
)

// XMLValue reads an XML answer as the JSON-shaped value a selector path walks: attributes as "@name", text as "#text",
// repeated elements as a list.
func XMLValue(body []byte) (any, error) {
	decoder := xml.NewDecoder(bytes.NewReader(body))
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		if start, ok := token.(xml.StartElement); ok {
			value, err := xmlElement(decoder, start)
			return map[string]any{start.Name.Local: value}, err
		}
	}
}

func xmlElement(decoder *xml.Decoder, start xml.StartElement) (any, error) {
	object := map[string]any{}
	for _, attribute := range start.Attr {
		object["@"+attribute.Name.Local] = attribute.Value
	}
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		switch one := token.(type) {
		case xml.StartElement:
			child, err := xmlElement(decoder, one)
			if err != nil {
				return nil, err
			}
			switch existing := object[one.Name.Local].(type) {
			case nil:
				object[one.Name.Local] = child
			case []any:
				object[one.Name.Local] = append(existing, child)
			default:
				object[one.Name.Local] = []any{existing, child}
			}
		case xml.CharData:
			text.Write(one)
		case xml.EndElement:
			trimmed := strings.TrimSpace(text.String())
			if len(object) == 0 {
				return trimmed, nil
			}
			if trimmed != "" {
				object["#text"] = trimmed
			}
			return object, nil
		}
	}
}
