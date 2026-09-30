package sources

import "encoding/json"

func encodeHeaders(h map[string]string) string {
	b, _ := json.Marshal(h)
	return string(b)
}

func (m *Manager) decryptHeaders(enc string) (map[string]string, error) {
	if enc == "" {
		return nil, nil
	}
	plain, err := m.box.Decrypt(enc)
	if err != nil {
		return nil, err
	}
	var h map[string]string
	if err := json.Unmarshal([]byte(plain), &h); err != nil {
		return nil, err
	}
	return h, nil
}
