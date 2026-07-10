use std::time::Duration;

// Blocking write/read calls have no cross-process wakeup mechanism to rely
// on (the shared storage is just bytes), so they poll the head/tail
// counters with a backoff instead of blocking on a futex/condvar. These
// constants bound that backoff.
const DEFAULT_MIN_POLL_INTERVAL: Duration = Duration::from_micros(50);
const DEFAULT_MAX_POLL_INTERVAL: Duration = Duration::from_millis(2);

/// Tunable parameters shared by [`Writer`](crate::Writer) and
/// [`Reader`](crate::Reader).
///
/// Blocking calls wait for free space/data by polling the shared head/tail
/// counters with an exponential backoff: the wait starts at `min_poll` and
/// doubles up to `max_poll` between each check. Smaller values reduce
/// latency at the cost of burning more CPU while waiting.
#[derive(Debug, Clone, Copy)]
pub struct Options {
    pub min_poll: Duration,
    pub max_poll: Duration,
}

impl Default for Options {
    fn default() -> Self {
        Options {
            min_poll: DEFAULT_MIN_POLL_INTERVAL,
            max_poll: DEFAULT_MAX_POLL_INTERVAL,
        }
    }
}

impl Options {
    /// Returns `Options` with the given poll backoff range. Zero values are
    /// replaced by the default for that bound; `max_poll` is raised to
    /// `min_poll` if it would otherwise be smaller.
    pub fn with_poll_interval(min_poll: Duration, max_poll: Duration) -> Self {
        let mut o = Options::default();
        if !min_poll.is_zero() {
            o.min_poll = min_poll;
        }
        if !max_poll.is_zero() {
            o.max_poll = max_poll;
        }
        if o.max_poll < o.min_poll {
            o.max_poll = o.min_poll;
        }
        o
    }
}
