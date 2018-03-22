package freeswitch

import "strings"

type result struct {
	*rawPacket
}

func (r *result) String() string {
	return r.body
}

type reply struct {
	*rawPacket
}

func (r *reply) ok() bool {
	return strings.HasPrefix(r.String(), "+OK")
}

func (r *reply) String() string {
	return r.headers.get("Reply-Text")
}

func (r *reply) jobId() (uuid string) {
	return r.headers.get("Job-UUID")
}

type disconnectNotice struct { // TODO: read entire packet?
	*rawPacket
}

func (dn *disconnectNotice) String() string {
	return dn.body
}
