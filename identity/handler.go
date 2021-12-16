package identity

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/ory/herodot"

	"github.com/tidwall/gjson"

	"github.com/julienschmidt/httprouter"
	"github.com/pkg/errors"

	"github.com/ory/kratos/driver/config"
	"github.com/ory/kratos/x"

	"github.com/ory/x/jsonx"
	"github.com/ory/x/sqlcon"
	"github.com/ory/x/sqlxx"
	"github.com/ory/x/urlx"
)

const RouteCollection = "/identities"
const RouteItem = RouteCollection + "/:id"
const RouteLookup = "/identity-lookup/:email"
const RouteKnownCredentials = "/credentials/known"

type (
	handlerDependencies interface {
		PoolProvider
		PrivilegedPoolProvider
		ManagementProvider
		x.WriterProvider
		config.Provider
		x.CSRFProvider
	}
	HandlerProvider interface {
		IdentityHandler() *Handler
	}
	Handler struct {
		r handlerDependencies
	}
)

func NewHandler(r handlerDependencies) *Handler {
	return &Handler{r: r}
}

func (h *Handler) RegisterAdminRoutes(admin *x.RouterAdmin) {
	admin.GET(RouteCollection, h.list)
	admin.GET(RouteItem, h.get)
	admin.GET(RouteLookup, h.lookup)
	admin.DELETE(RouteItem, h.delete)

	admin.POST(RouteCollection, h.create)
	admin.PUT(RouteItem, h.update)

	admin.POST(RouteKnownCredentials, h.knownCredentials)
}

// A single login method and provider.
//
// swagger:response knownCredentialsMethod
// nolint:deadcode,unused
type knownCredentialsMethod struct {
	// in: body
	Method string `json:"method"`
	// in: body
	Username string `json:"username,omitempty"`
	// in: body
	Provider string `json:"provider,omitempty"`
}

// Response object for knownCredentials
//
// swagger:response knownCredentialsResponse
// nolint:deadcode,unused
type knownCredentialsResponse struct {
	// in: body
	// required: true
	Found bool `json:"found"`
	// in: body
	// type: array
	Methods []knownCredentialsMethod `json:"methods,omitempty"`
}

// Request object for knownCredentials
type knownCredentialsRequest struct {
	Identifier string `json:"identifier"`
	Method     string `json:"method"`
}

var ErrSpecifyIdentifier = herodot.DefaultError{
	ErrorField: "must specify identifier",
	CodeField:  http.StatusBadRequest,
}

var ErrInvalidMethod = herodot.DefaultError{
	ErrorField: "method must be either 'password' or 'oidc'",
	CodeField:  http.StatusBadRequest,
}

// swagger:route GET /credentials/known admin knownCredentialsRequest
//
// Check if a specified identifier has been previously registered with the specified method, or optionally search
// for the method and provider if a method is not specified.
//
//     Produces:
//     - application/json
//
//     Schemes: http, https
//
//     Responses:
//       200: knownCredentialsResponse
//       500: genericError
func (h *Handler) knownCredentials(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	ctx := r.Context()
	kcr := knownCredentialsRequest{}
	if err := jsonx.NewStrictDecoder(r.Body).Decode(&kcr); err != nil {
		h.r.Writer().WriteErrorCode(w, r, http.StatusBadRequest, errors.WithStack(err))
		return
	}

	if kcr.Identifier == "" {
		h.r.Writer().WriteErrorCode(w, r, http.StatusBadRequest, ErrSpecifyIdentifier)
		return
	}

	if kcr.Method != "" && kcr.Method != CredentialsTypeOIDC.String() && kcr.Method != CredentialsTypePassword.String() {
		h.r.Writer().WriteErrorCode(w, r, http.StatusBadRequest, ErrInvalidMethod)
		return
	}

	address, err := h.r.PrivilegedIdentityPool().FindVerifiableAddressByValue(ctx, VerifiableAddressTypeEmail, kcr.Identifier)
	if err != nil {
		if errors.Is(err, sqlcon.ErrNoRows) {
			address = nil
		} else {
			h.r.Writer().WriteErrorCode(w, r, http.StatusInternalServerError, err)
			return
		}
	}

	log.Printf("knownCredentials: address: %v\n", address)
	var identity *Identity
	if address != nil {
		identity, err = h.r.PrivilegedIdentityPool().GetIdentityConfidential(ctx, address.IdentityID)
		if err != nil {
			if errors.Is(err, sqlcon.ErrNoRows) {
				identity = nil
			} else {
				h.r.Writer().WriteErrorCode(w, r, http.StatusInternalServerError, err)
				return
			}
		}
	}

	log.Printf("knownCredentials: identity: %v\n", identity)
	result := knownCredentialsResponse{false, []knownCredentialsMethod{}}
	if kcr.Method == CredentialsTypePassword.String() || kcr.Method == "" {
		// if the credentials can be looked up directly by identifier then they're type password
		_, _, err := h.r.PrivilegedIdentityPool().FindByCredentialsIdentifier(ctx, CredentialsTypePassword, kcr.Identifier)
		if err == nil {
			result.Found = true
			result.Methods = append(result.Methods, knownCredentialsMethod{
				CredentialsTypePassword.String(),
				"",
				"",
			})
		} else {
			creds, ok := identity.GetCredentials(CredentialsTypePassword)
			if ok {
				if creds.Identifiers != nil &&
					len(creds.Identifiers) > 0 {
					// didn't find the credentials by identifier but we found them via email, so maybe they have a username.
					// we should return the username in the response so we can show the user
					result.Found = true
					result.Methods = append(result.Methods, knownCredentialsMethod{
						CredentialsTypePassword.String(),
						creds.Identifiers[0],
						"",
					})
				} else {
					// no credentials found, but the identity exists, so this user needs to verify their email
					// and pick a way to sign in
					result.Found = true
					result.Methods = append(result.Methods, knownCredentialsMethod{
						CredentialsTypeNone.String(),
						"",
						"",
					})
				}
			}
		}
	}

	if kcr.Method == CredentialsTypeOIDC.String() || kcr.Method == "" {
		if identity != nil {
			creds, ok := identity.GetCredentials(CredentialsTypeOIDC)
			if ok {
				providers := gjson.Get(string(creds.Config), "providers")
				result.Found = true
				for _, provider := range providers.Array() {
					result.Methods = append(result.Methods, knownCredentialsMethod{
						CredentialsTypeOIDC.String(),
						"",
						provider.Get("provider").String(),
					})
				}
			} else if !result.Found {
				// identity already exists but no credentials found and we didn't already put this in the result
				result.Found = true
				result.Methods = append(result.Methods, knownCredentialsMethod{
					CredentialsTypeNone.String(),
					"",
					"",
				})
			}
		}
	}

	h.r.Writer().Write(w, r, &result)
}

func (h *Handler) RegisterPublicRoutes(public *x.RouterPublic) {
	h.r.CSRFHandler().IgnoreGlobs(RouteCollection, RouteCollection+"/*")
	public.GET(RouteCollection, x.RedirectToAdminRoute(h.r))
	public.GET(RouteItem, x.RedirectToAdminRoute(h.r))
	public.DELETE(RouteItem, x.RedirectToAdminRoute(h.r))
	public.POST(RouteCollection, x.RedirectToAdminRoute(h.r))
	public.PUT(RouteItem, x.RedirectToAdminRoute(h.r))
}

// A list of identities.
// swagger:model identityList
// nolint:deadcode,unused
type identityList []Identity

// swagger:parameters adminListIdentities
// nolint:deadcode,unused
type adminListIdentities struct {
	// Items per Page
	//
	// This is the number of items per page.
	//
	// required: false
	// in: query
	// default: 100
	// min: 1
	// max: 500
	PerPage int `json:"per_page"`

	// Pagination Page
	//
	// required: false
	// in: query
	// default: 0
	// min: 0
	Page int `json:"page"`
}

// swagger:route GET /identities v0alpha1 adminListIdentities
//
// List Identities
//
// Lists all identities. Does not support search at the moment.
//
// Learn how identities work in [Ory Kratos' User And Identity Model Documentation](https://www.ory.sh/docs/next/kratos/concepts/identity-user-model).
//
//     Produces:
//     - application/json
//
//     Schemes: http, https
//
//     Security:
//       oryAccessToken:
//
//     Responses:
//       200: identityList
//       500: jsonError
func (h *Handler) list(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	page, itemsPerPage := x.ParsePagination(r)
	is, err := h.r.IdentityPool().ListIdentities(r.Context(), page, itemsPerPage)
	if err != nil {
		h.r.Writer().WriteError(w, r, err)
		return
	}

	total, err := h.r.IdentityPool().CountIdentities(r.Context())
	if err != nil {
		h.r.Writer().WriteError(w, r, err)
		return
	}

	x.PaginationHeader(w, urlx.AppendPaths(h.r.Config(r.Context()).SelfAdminURL(), RouteCollection), total, page, itemsPerPage)
	h.r.Writer().Write(w, r, is)
}

// swagger:parameters adminGetIdentity
// nolint:deadcode,unused
type adminGetIdentity struct {
	// ID must be set to the ID of identity you want to get
	//
	// required: true
	// in: path
	ID string `json:"id"`
}

// swagger:route GET /identities/{id} v0alpha1 adminGetIdentity
//
// Get an Identity
//
// Learn how identities work in [Ory Kratos' User And Identity Model Documentation](https://www.ory.sh/docs/next/kratos/concepts/identity-user-model).
//
//     Consumes:
//     - application/json
//
//     Produces:
//     - application/json
//
//     Schemes: http, https
//
//     Security:
//       oryAccessToken:
//
//     Responses:
//       200: identity
//       404: jsonError
//       500: jsonError
func (h *Handler) get(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	i, err := h.r.PrivilegedIdentityPool().GetIdentityConfidential(r.Context(), x.ParseUUID(ps.ByName("id")))
	if err != nil {
		h.r.Writer().WriteError(w, r, err)
		return
	}

	h.r.Writer().Write(w, r, IdentityWithCredentialsMetadataInJSON(*i))
}

func (h *Handler) lookup(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	va, err := h.r.PrivilegedIdentityPool().FindVerifiableAddressByValue(r.Context(), VerifiableAddressTypeEmail, ps.ByName("email"))
	if err != nil {
		h.r.Writer().WriteError(w, r, err)
		return
	}
	if !va.Verified {
		h.r.Writer().WriteErrorCode(w, r, http.StatusNotFound, errors.Errorf("Email is unverified"))
		return
	}

	i, err := h.r.PrivilegedIdentityPool().GetIdentityConfidential(r.Context(), va.IdentityID)
	if err != nil {
		h.r.Writer().WriteError(w, r, err)
		return
	}

	h.r.Writer().Write(w, r, IdentityWithCredentialsMetadataInJSON(*i))
}

// swagger:parameters adminCreateIdentity
// nolint:deadcode,unused
type adminCreateIdentity struct {
	// in: body
	Body AdminCreateIdentityBody
}

// swagger:model adminCreateIdentityBody
type AdminCreateIdentityBody struct {
	// SchemaID is the ID of the JSON Schema to be used for validating the identity's traits.
	//
	// required: true
	SchemaID string `json:"schema_id"`

	// Traits represent an identity's traits. The identity is able to create, modify, and delete traits
	// in a self-service manner. The input will always be validated against the JSON Schema defined
	// in `schema_url`.
	//
	// required: true
	Traits json.RawMessage `json:"traits"`
}

// swagger:route POST /identities v0alpha1 adminCreateIdentity
//
// Create an Identity
//
// This endpoint creates an identity. It is NOT possible to set an identity's credentials (password, ...)
// using this method! A way to achieve that will be introduced in the future.
//
// Learn how identities work in [Ory Kratos' User And Identity Model Documentation](https://www.ory.sh/docs/next/kratos/concepts/identity-user-model).
//
//     Consumes:
//     - application/json
//
//     Produces:
//     - application/json
//
//     Schemes: http, https
//
//     Security:
//       oryAccessToken:
//
//     Responses:
//       201: identity
//       400: jsonError
//		 409: jsonError
//       500: jsonError
func (h *Handler) create(w http.ResponseWriter, r *http.Request, _ httprouter.Params) {
	var cr AdminCreateIdentityBody
	if err := jsonx.NewStrictDecoder(r.Body).Decode(&cr); err != nil {
		h.r.Writer().WriteErrorCode(w, r, http.StatusBadRequest, errors.WithStack(err))
		return
	}

	i := &Identity{SchemaID: cr.SchemaID, Traits: []byte(cr.Traits), State: StateActive, StateChangedAt: sqlxx.NullTime(time.Now())}
	if err := h.r.IdentityManager().Create(r.Context(), i); err != nil {
		h.r.Writer().WriteError(w, r, err)
		return
	}

	h.r.Writer().WriteCreated(w, r,
		urlx.AppendPaths(
			h.r.Config(r.Context()).SelfAdminURL(),
			"identities",
			i.ID.String(),
		).String(),
		i,
	)
}

// swagger:parameters adminUpdateIdentity
// nolint:deadcode,unused
type adminUpdateIdentity struct {
	// ID must be set to the ID of identity you want to update
	//
	// required: true
	// in: path
	ID string `json:"id"`

	// in: body
	Body AdminUpdateIdentityBody
}

type AdminUpdateIdentityBody struct {
	// SchemaID is the ID of the JSON Schema to be used for validating the identity's traits. If set
	// will update the Identity's SchemaID.
	SchemaID string `json:"schema_id"`

	// Traits represent an identity's traits. The identity is able to create, modify, and delete traits
	// in a self-service manner. The input will always be validated against the JSON Schema defined
	// in `schema_id`.
	//
	// required: true
	Traits json.RawMessage `json:"traits"`

	// State is the identity's state.
	//
	// required: true
	State State `json:"state"`
}

// swagger:route PUT /identities/{id} v0alpha1 adminUpdateIdentity
//
// Update an Identity
//
// This endpoint updates an identity. It is NOT possible to set an identity's credentials (password, ...)
// using this method! A way to achieve that will be introduced in the future.
//
// The full identity payload (except credentials) is expected. This endpoint does not support patching.
//
// Learn how identities work in [Ory Kratos' User And Identity Model Documentation](https://www.ory.sh/docs/next/kratos/concepts/identity-user-model).
//
//     Consumes:
//     - application/json
//
//     Produces:
//     - application/json
//
//     Schemes: http, https
//
//     Security:
//       oryAccessToken:
//
//     Responses:
//       200: identity
//       400: jsonError
//       404: jsonError
//		 409: jsonError
//       500: jsonError
func (h *Handler) update(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	var ur AdminUpdateIdentityBody
	if err := errors.WithStack(jsonx.NewStrictDecoder(r.Body).Decode(&ur)); err != nil {
		h.r.Writer().WriteError(w, r, err)
		return
	}

	id := x.ParseUUID(ps.ByName("id"))
	identity, err := h.r.PrivilegedIdentityPool().GetIdentityConfidential(r.Context(), id)
	if err != nil {
		h.r.Writer().WriteError(w, r, err)
		return
	}

	if ur.SchemaID != "" {
		identity.SchemaID = ur.SchemaID
	}

	if ur.State != "" && identity.State != ur.State {
		if err := ur.State.IsValid(); err != nil {
			h.r.Writer().WriteError(w, r, herodot.ErrBadRequest.WithReasonf("%s", err).WithWrap(err))
		}

		identity.State = ur.State
		identity.StateChangedAt = sqlxx.NullTime(time.Now())
	}

	identity.Traits = []byte(ur.Traits)
	if err := h.r.IdentityManager().Update(
		r.Context(),
		identity,
		ManagerAllowWriteProtectedTraits,
	); err != nil {
		h.r.Writer().WriteError(w, r, err)
		return
	}

	h.r.Writer().Write(w, r, identity)
}

// swagger:parameters adminDeleteIdentity
// nolint:deadcode,unused
type adminDeleteIdentity struct {
	// ID is the identity's ID.
	//
	// required: true
	// in: path
	ID string `json:"id"`
}

// swagger:route DELETE /identities/{id} v0alpha1 adminDeleteIdentity
//
// Delete an Identity
//
// Calling this endpoint irrecoverably and permanently deletes the identity given its ID. This action can not be undone.
// This endpoint returns 204 when the identity was deleted or when the identity was not found, in which case it is
// assumed that is has been deleted already.
//
// Learn how identities work in [Ory Kratos' User And Identity Model Documentation](https://www.ory.sh/docs/next/kratos/concepts/identity-user-model).
//
//     Produces:
//     - application/json
//
//     Schemes: http, https
//
//     Security:
//       oryAccessToken:
//
//     Responses:
//       204: emptyResponse
//       404: jsonError
//       500: jsonError
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	if err := h.r.IdentityPool().(PrivilegedPool).DeleteIdentity(r.Context(), x.ParseUUID(ps.ByName("id"))); err != nil {
		h.r.Writer().WriteError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
