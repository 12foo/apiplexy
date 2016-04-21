package hmac

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"github.com/12foo/apiplexy"
	"github.com/satori/go.uuid"
	"net/http"
	"strings"
)

type HMACAuthPlugin struct {
}

var availableTypes = []apiplexy.KeyType{
	{Name: "HMAC", Description: "HMAC-based request signing using the HTTP Date header."},
}

func (auth *HMACAuthPlugin) AvailableTypes() []apiplexy.KeyType {
	return availableTypes
}

func (auth *HMACAuthPlugin) Generate(keyType string) (key apiplexy.Key, err error) {
	if keyType != "HMAC" {
		return apiplexy.Key{}, fmt.Errorf("Unknown key type: %s", keyType)
	}
	data := map[string]interface{}{
		"secret": base64.StdEncoding.EncodeToString(uuid.NewV4().Bytes()),
	}
	k := apiplexy.Key{
		ID:   base64.StdEncoding.EncodeToString(uuid.NewV4().Bytes()),
		Type: "HMAC",
		Data: data,
	}
	return k, nil
}

func (auth *HMACAuthPlugin) Detect(req *http.Request, ctx *apiplexy.APIContext) (maybeKey string, keyType string, bits map[string]interface{}, err error) {
	if !strings.HasPrefix(req.Header.Get("Authorization"), "Signature ") {
		return "", "", nil, nil
	}
	sigParts := strings.Split(strings.TrimPrefix(req.Header.Get("Authorization"), "Signature "), ",")
	sig := make(map[string]interface{}, len(sigParts))
	for _, part := range sigParts {
		p := strings.SplitN(part, "=", 2)
		if len(p) != 2 {
			return "", "", nil, nil
		}
		p[0] = strings.Trim(p[0], "\" ")
		p[1] = strings.Trim(p[1], "\" ")
		sig[p[0]] = p[1]
	}
	if sig["keyId"] == nil || sig["signature"] == nil {
		return "", "", nil, nil
	}
	return sig["keyId"].(string), "HMAC", sig, nil
}

func (auth *HMACAuthPlugin) Validate(key *apiplexy.Key, req *http.Request, ctx *apiplexy.APIContext, bits map[string]interface{}) (isValid bool, err error) {
	mac := hmac.New(sha1.New, []byte(key.Data["secret"].(string)))
	mac.Write([]byte(req.Header.Get("Date")))
	sig, _ := base64.StdEncoding.DecodeString(bits["signature"].(string))
	return hmac.Equal(mac.Sum(nil), sig), nil
}

func (auth *HMACAuthPlugin) DefaultConfig() map[string]interface{} {
	// TODO make location of HMAC signature configurable
	return nil
}

func (auth *HMACAuthPlugin) Configure(config map[string]interface{}) error {
	return nil
}

func init() {
	// _ = apiplexy.AuthPlugin(&HMACAuthPlugin{})
	apiplexy.RegisterPlugin(
		"hmac",
		"Authenticate requests via HMAC.",
		"https://github.com/12foo/apiplexy/tree/master/auth/hmac",
		HMACAuthPlugin{},
	)
}
