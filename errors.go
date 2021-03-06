package freeswitch

type fsError string

func (e fsError) Error() string {
	return string(e)
}

// These errors, amongst others, may be returned by a failing or closing client connection.
const (
	EAlreadyConnected     fsError = "already connected"
	EAuthenticationFailed fsError = "authentication failed"
	EBlankHostname        fsError = "hostname cannot be blank"
	ECommandFailed        fsError = "command failed"
	EDisconnected         fsError = "host sent disconnection notice"
	ENotConnected         fsError = "not connected"
	EShutdown             fsError = "shutdown was requested"
	ETimeout              fsError = "timeout"
	EUnexpectedResponse   fsError = "unexpected response from FreeSWITCH"
)
