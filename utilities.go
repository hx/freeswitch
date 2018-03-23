package freeswitch

import (
	"math/rand"
	"sync"
)

func exclusive(lock sync.Locker, f func()) {
	lock.Lock()
	defer lock.Unlock()
	f()
}

const uniqIdPool = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"

func uniqId() string {
	var (
		n      = int32(len(uniqIdPool))
		result = make([]byte, 32)
	)
	for i := range result {
		result[i] = uniqIdPool[rand.Int31n(n)]
	}
	return string(result)
}

func eventsSubscriptionCommand(eventNames ...EventName) (cmd []string) {
	cmd = []string{"events", "plain"}
	if len(eventNames) > 0 {
		var (
			normal []string
			custom []string
		)
		for _, name := range eventNames {
			if name.IsCustom() {
				custom = append(custom, name.Subclass)
			} else {
				normal = append(normal, name.Name)
			}
		}
		cmd = append(cmd, normal...)
		if len(custom) > 0 {
			cmd = append(cmd, append([]string{"CUSTOM"}, custom...)...)
		}
	} else {
		cmd = append(cmd, "all")
	}
	return
}
