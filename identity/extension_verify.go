package identity

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ory/jsonschema/v3"

	"github.com/ory/kratos/schema"
)

type SchemaExtensionVerification struct {
	lifespan time.Duration
	l        sync.Mutex
	v        []VerifiableAddress
	i        *Identity
}

func NewSchemaExtensionVerification(i *Identity, lifespan time.Duration) *SchemaExtensionVerification {
	return &SchemaExtensionVerification{i: i, lifespan: lifespan}
}

func (r *SchemaExtensionVerification) Run(ctx jsonschema.ValidationContext, s schema.ExtensionConfig, value interface{}) error {
	r.l.Lock()
	defer r.l.Unlock()

	log.Printf("DEBUGDEBUG: SchemaExtensionVerification.Run() 1: i.VerifiableAddresses: %v\n", r.i.VerifiableAddresses)

	switch s.Verification.Via {
	case "email":
		if !jsonschema.Formats["email"](value) {
			return ctx.Error("format", "%q is not valid %q", value, "email")
		}

		address := NewVerifiableEmailAddress(fmt.Sprintf("%s", value), r.i.ID)
		log.Printf("DEBUGDEBUG: SchemaExtensionVerification.Run() 2: address: %v\n", address)
		log.Printf("DEBUGDEBUG: SchemaExtensionVerification.Run() 2: r.i.VerifiableAddresses: %v\n", r.i.VerifiableAddresses)

		if has := r.has(r.i.VerifiableAddresses, address); has != nil {
			if r.has(r.v, address) == nil {
				log.Printf("DEBUGDEBUG: SchemaExtensionVerification.Run() 3: append has: %v\n", has)
				r.v = append(r.v, *has)
				log.Printf("DEBUGDEBUG: SchemaExtensionVerification.Run() 4: append r.v: %v\n", r.v)
			}
			return nil
		}

		if has := r.has(r.v, address); has == nil {
			log.Printf("DEBUGDEBUG: SchemaExtensionVerification.Run() 5: append has: %v\n", has)
			r.v = append(r.v, *address)
			log.Printf("DEBUGDEBUG: SchemaExtensionVerification.Run() 6: append has: %v\n", r.v)
		}

		return nil
	case "":
		return nil
	}

	return ctx.Error("", "verification.via has unknown value %q", s.Verification.Via)
}

func (r *SchemaExtensionVerification) has(haystack []VerifiableAddress, needle *VerifiableAddress) *VerifiableAddress {
	for _, has := range haystack {
		if has.Value == needle.Value && has.Via == needle.Via {
			return &has
		}
	}
	return nil
}

func (r *SchemaExtensionVerification) Finish() error {
	log.Printf("DEBUGDEBUG: SchemaExtensionVerification.Finish(): r.i.VerifiableAddresses: %v\n", r.i.VerifiableAddresses)
	log.Printf("DEBUGDEBUG: SchemaExtensionVerification.Finish(): r.v: %v\n", r.v)
	r.i.VerifiableAddresses = r.v
	return nil
}
