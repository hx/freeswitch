package freeswitch

type packetType string

const (
	ptAuthRequest      packetType = "auth/request"
	ptCommandReply     packetType = "command/reply"
	ptDisconnectNotice packetType = "text/disconnect-notice"
	ptResult           packetType = "api/response"
	ptEventPlain       packetType = "text/event-plain"
	ptEventJSON        packetType = "text/event-json" // Unused
)

type rawPacket struct {
	headers headers
	body    string
}

type packet interface {
	String() string
	packetType() packetType
}

func (rp *rawPacket) packetType() packetType {
	return packetType(rp.headers.get("Content-Type"))
}

func (rp *rawPacket) String() string {
	return rp.headers.String() + "\n" + rp.body
}

func (rp *rawPacket) cast() packet {
	switch rp.packetType() {
	case ptCommandReply:
		return &reply{rp}
	case ptEventPlain:
		return &inboundEvent{rawPacket: rp}
	case ptResult:
		return &result{rp}
	case ptDisconnectNotice:
		return &disconnectNotice{rp}
	default:
		return rp
	}
}
