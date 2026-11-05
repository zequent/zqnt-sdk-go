//go:build integration

package edgelink

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/mem"
	"google.golang.org/protobuf/encoding/protowire"
)

// platform is the administrator's side of connector for this test: it provisions the licensed
// organization and mints the claim code the node enrolls with. Both RPCs belong to the platform's
// internal contracts (zqnt-contracts, private), so the two requests are written on the wire
// directly instead of pulling those contracts into the SDK.
type platform struct {
	t    *testing.T
	conn *grpc.ClientConn
}

const (
	organizationService = "/zqnt.organization.v3.OrganizationService/ProvisionLicensedOrganization"
	assetClaimService   = "/zqnt.asset.v3.AssetClaimService/CreateClaim"
)

// Field numbers of the messages written below, as the internal protos define them.
const (
	provisionOrganizationID = 2
	provisionName           = 3
	provisionActivation     = 5
	activationOrganization  = 1
	activationID            = 2
	activationSealedToken   = 3
	activationScheme        = 4

	claimOrganizationID = 2
	claimLabel          = 3
	claimMaxRedemptions = 6
	claimTTLSeconds     = 7
	claimResponseCode   = 2
)

func (s *stack) platform() *platform {
	s.t.Helper()
	token := serviceToken(s.t, s.serviceKey)
	conn, err := grpc.NewClient(fmt.Sprintf("127.0.0.1:%d", s.ports.connector),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(bearer(token)))
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { _ = conn.Close() })
	return &platform{t: s.t, conn: conn}
}

// serviceToken is a platform service token (scope platform, no organization) signed with the
// installation's service key, as one core service presents to another.
func serviceToken(t *testing.T, key ed25519.PrivateKey) string {
	t.Helper()
	now := time.Now()
	return signJWS(t, key, map[string]any{
		"iss":   "zqnt-service",
		"sub":   "svc:edge-link-toxiproxy-test",
		"aud":   "zqnt-platform",
		"scope": []string{"platform"},
		"iat":   now.Unix(),
		"exp":   now.Add(15 * time.Minute).Unix(),
		"jti":   uuid.NewString(),
	})
}

type bearer string

func (b bearer) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + string(b)}, nil
}

func (bearer) RequireTransportSecurity() bool { return false }

func (p *platform) provisionOrganization(ctx context.Context, organization, name string) {
	p.t.Helper()
	var activation []byte
	activation = appendString(activation, activationOrganization, organization)
	activation = appendString(activation, activationID, "act-"+uuid.NewString()[:8])
	activation = appendBytes(activation, activationSealedToken, []byte("edge-link-toxiproxy"))
	activation = appendString(activation, activationScheme, "none")
	var request []byte
	request = appendString(request, provisionOrganizationID, organization)
	request = appendString(request, provisionName, name)
	request = appendBytes(request, provisionActivation, activation)
	if _, err := p.invoke(ctx, organizationService, request); err != nil {
		p.t.Fatalf("provision organization: %v", err)
	}
}

// createClaim mints a one-time claim code for an infrastructure node of the organization.
func (p *platform) createClaim(ctx context.Context, organization string) string {
	p.t.Helper()
	var request []byte
	request = appendString(request, claimOrganizationID, organization)
	request = appendString(request, claimLabel, "edge-link toxiproxy test")
	request = appendVarint(request, claimMaxRedemptions, 1)
	request = appendVarint(request, claimTTLSeconds, 3600)
	response, err := p.invoke(ctx, assetClaimService, request)
	if err != nil {
		p.t.Fatalf("create claim: %v", err)
	}
	code, err := stringField(response, claimResponseCode)
	if err != nil || code == "" {
		p.t.Fatalf("create claim: no code in the response: %v", err)
	}
	return code
}

func (p *platform) invoke(ctx context.Context, method string, request []byte) ([]byte, error) {
	var response []byte
	err := p.conn.Invoke(ctx, method, request, &response, grpc.ForceCodecV2(rawCodec{}))
	return response, err
}

func appendString(b []byte, field protowire.Number, s string) []byte {
	b = protowire.AppendTag(b, field, protowire.BytesType)
	return protowire.AppendString(b, s)
}

func appendBytes(b []byte, field protowire.Number, v []byte) []byte {
	b = protowire.AppendTag(b, field, protowire.BytesType)
	return protowire.AppendBytes(b, v)
}

func appendVarint(b []byte, field protowire.Number, v uint64) []byte {
	b = protowire.AppendTag(b, field, protowire.VarintType)
	return protowire.AppendVarint(b, v)
}

// stringField returns the first string field number want of a message, or "" when absent.
func stringField(msg []byte, want protowire.Number) (string, error) {
	for len(msg) > 0 {
		num, typ, n := protowire.ConsumeTag(msg)
		if n < 0 {
			return "", protowire.ParseError(n)
		}
		msg = msg[n:]
		if typ == protowire.BytesType && num == want {
			v, n := protowire.ConsumeBytes(msg)
			if n < 0 {
				return "", protowire.ParseError(n)
			}
			return string(v), nil
		}
		n = protowire.ConsumeFieldValue(num, typ, msg)
		if n < 0 {
			return "", protowire.ParseError(n)
		}
		msg = msg[n:]
	}
	return "", errors.New("field not present")
}

// rawCodec passes serialized messages through unchanged.
type rawCodec struct{}

func (rawCodec) Name() string { return "proto" }

func (rawCodec) Marshal(v any) (mem.BufferSlice, error) {
	b, ok := v.([]byte)
	if !ok {
		return nil, fmt.Errorf("cannot send %T", v)
	}
	return mem.BufferSlice{mem.SliceBuffer(b)}, nil
}

func (rawCodec) Unmarshal(data mem.BufferSlice, v any) error {
	p, ok := v.(*[]byte)
	if !ok {
		return fmt.Errorf("cannot receive into %T", v)
	}
	*p = data.Materialize()
	return nil
}
