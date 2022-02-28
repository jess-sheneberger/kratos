package template

import (
	"encoding/json"
)

type (
	VerificationValidCode struct {
		c TemplateConfig
		m *VerificationValidModelCode
	}
	VerificationValidModelCode struct {
		To              string
		VerificationURL string
		Code            string
		Identity        map[string]interface{}
	}
)

func NewVerificationValidCode(c TemplateConfig, m *VerificationValidModelCode) *VerificationValidCode {
	return &VerificationValidCode{c: c, m: m}
}

func (t *VerificationValidCode) EmailRecipient() (string, error) {
	return t.m.To, nil
}

func (t *VerificationValidCode) EmailSubject() (string, error) {
	return loadTextTemplate(t.c.CourierTemplatesRoot(), "verification_code/valid/email.subject.gotmpl", "verification_code/valid/email.subject*", t.m)
}

func (t *VerificationValidCode) EmailBody() (string, error) {
	return loadTextTemplate(t.c.CourierTemplatesRoot(), "verification_code/valid/email.body.gotmpl", "verification_code/valid/email.body*", t.m)
}

func (t *VerificationValidCode) EmailBodyPlaintext() (string, error) {
	return loadTextTemplate(t.c.CourierTemplatesRoot(), "verification_code/valid/email.body.plaintext.gotmpl", "verification_code/valid/email.body.plaintext*", t.m)
}

func (t *VerificationValidCode) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.m)
}
