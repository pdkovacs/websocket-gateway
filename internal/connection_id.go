package wsgw

import "github.com/rs/xid"

type connectionID string

func createID() connectionID {
	return connectionID(xid.New().String())
}
