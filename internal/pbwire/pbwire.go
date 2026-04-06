package pbwire

import (
	"fmt"

	pocketbridgev1 "github.com/cagedbird043/pocket-bridge/gen/go/pocketbridge/v1"
	"google.golang.org/protobuf/proto"
)

func MarshalEnvelope(env *pocketbridgev1.Envelope) ([]byte, error) {
	if env == nil {
		return nil, fmt.Errorf("nil envelope")
	}
	return proto.Marshal(env)
}

func UnmarshalEnvelope(data []byte) (*pocketbridgev1.Envelope, error) {
	var env pocketbridgev1.Envelope
	if err := proto.Unmarshal(data, &env); err != nil {
		return nil, err
	}
	return &env, nil
}
