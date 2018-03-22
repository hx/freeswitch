package freeswitch

type fsError string

func (e fsError) Error() string {
	return string(e)
}

const (
	EAlreadyConnected     fsError = "already connected"
	EAuthenticationFailed fsError = "authentication failed"
	EBlankHostname        fsError = "hostname cannot be blank"
	ECommandFailed        fsError = "command failed"
	EInvalidPort          fsError = "port must be between 1 and 65535"
	ENotConnected         fsError = "not connected"
	ETimeout              fsError = "timeout"
	EUnexpectedResponse   fsError = "unexpected response from FreeSWITCH"
)
