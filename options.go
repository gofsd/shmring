package shmring

import "time"

// Blocking Write/Read calls have no cross-process wakeup mechanism to rely
// on (the shared storage is just bytes), so they poll the head/tail
// counters with a backoff instead of blocking on a futex/condvar. These
// constants bound that backoff.
const (
	defaultMinPollInterval = 50 * time.Microsecond
	defaultMaxPollInterval = 2 * time.Millisecond
)

// options holds the tunable parameters shared by Writer and Reader.
type options struct {
	minPoll time.Duration
	maxPoll time.Duration
}

func defaultOptions() options {
	return options{
		minPoll: defaultMinPollInterval,
		maxPoll: defaultMaxPollInterval,
	}
}

// Option configures a Writer or Reader created by NewWriter, NewReader,
// CreateShm or OpenShm.
type Option func(*options)

// WithPollInterval sets the backoff range used while a blocking Write waits
// for free space, or a blocking Read waits for data. The wait starts at min
// and doubles up to max between each check of the shared head/tail
// counters. Smaller values reduce latency at the cost of burning more CPU
// while waiting.
func WithPollInterval(minInterval, maxInterval time.Duration) Option {
	return func(o *options) {
		if minInterval > 0 {
			o.minPoll = minInterval
		}
		if maxInterval > 0 {
			o.maxPoll = maxInterval
		}
		if o.maxPoll < o.minPoll {
			o.maxPoll = o.minPoll
		}
	}
}
