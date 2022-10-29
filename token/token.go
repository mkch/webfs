package token

import (
	"math/rand"
	"time"
)

const idRunes string = "ABCDEFGHJKLMNPQRSTUVWXY3456789"

func init() {
	rand.Seed(time.Now().UnixNano())
}

// 生成一个新的 token.
func New(length int) string {
	var bytes = make([]byte, length)
	for i := 0; i < length; i++ {
		bytes[i] = idRunes[rand.Int()%len(idRunes)]
	}
	return string(bytes[:])
}
