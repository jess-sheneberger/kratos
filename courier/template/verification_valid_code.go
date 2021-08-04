package template

import (
	"encoding/json"

	"github.com/ory/kratos/driver/config"
)

type (
	VerificationValidCode struct {
		c *config.Config
		m *VerificationValidModelCode
	}
	VerificationValidModelCode struct {
		To              string
		VerificationURL string
		Code            string
	}
)

func NewVerificationValidCode(c *config.Config, m *VerificationValidModelCode) *VerificationValidCode {
	return &VerificationValidCode{c: c, m: m}
}

func (t *VerificationValidCode) EmailRecipient() (string, error) {
	return t.m.To, nil
}

func (t *VerificationValidCode) EmailSubject() (string, error) {
	return loadTextTemplate(t.c.CourierTemplatesRoot(), "verification_code/valid/email.subject.gotmpl", t.m)
}

func (t *VerificationValidCode) EmailBody() (string, error) {
	return loadTextTemplate(t.c.CourierTemplatesRoot(), "verification_code/valid/email.body.gotmpl", t.m)
}

func (t *VerificationValidCode) EmailBodyPlaintext() (string, error) {
	return loadTextTemplate(t.c.CourierTemplatesRoot(), "verification_code/valid/email.body.plaintext.gotmpl", t.m)
}

func (t *VerificationValidCode) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.m)
}
