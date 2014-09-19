package e3x

import (
	"sort"
	"time"

	"bitbucket.org/simonmenke/go-telehash/transports"
)

const (
	c_MAX_ADDRESS_BOOK_ENTRIES = 16
)

type addressBook struct {
	id          int
	active      *addressBookEntry
	known       []*addressBookEntry
	unsupported []string
}

type addressBookEntry struct {
	Address     transports.Addr
	LastAttempt time.Time
	FirstSeen   time.Time
	LastSeen    time.Time
	Reachable   bool
	GotResponse bool

	latency      time.Duration
	samples      [16]time.Duration
	sample_count int
}

var nextAddressBookId = 0

func newAddressBook() *addressBook {
	nextAddressBookId++
	return &addressBook{id: nextAddressBookId}
}

func (book *addressBook) ActiveAddress() transports.Addr {
	if book.active == nil {
		return nil
	}

	return book.active.Address
}

func (book *addressBook) KnownAddresses() []transports.Addr {
	s := make([]transports.Addr, len(book.known))
	for i, e := range book.known {
		s[i] = e.Address
	}
	return s
}

func (book *addressBook) HandshakeAddresses() []transports.Addr {
	s := make([]transports.Addr, 0, len(book.known))
	for _, e := range book.known {
		if len(s) == 5 {
			break
		}
		if !e.Reachable {
			break
		}
		s = append(s, e.Address)
	}
	return s
}

func (book *addressBook) NextHandshakeEpoch() {
	var changed bool

	for _, e := range book.known {
		if !e.GotResponse {
			// no handshake since last epoch
			// mark as broken
			if e.Reachable {
				e.Reachable = false
				changed = true

				tracef("(id=%d) \x1B[31mDetected broken path\x1B[0m %s", book.id, e)
			}
		}

		e.GotResponse = false
	}

	if changed {
		book.updateActive()
	}
}

func (book *addressBook) SentHandshake(addr transports.Addr) {
	tracef("(id=%d) SentHandshake(%s)", book.id, addr)

	var (
		now = time.Now()
		idx = book.indexOf(addr)
	)

	if idx < 0 {
		return
	}

	e := book.known[idx]
	e.LastAttempt = now
}

func (book *addressBook) AddAddress(addr transports.Addr) {
	var (
		now = time.Now()
		idx = book.indexOf(addr)
		e   *addressBookEntry
	)

	if idx >= 0 {
		return
	}

	e = &addressBookEntry{Address: addr}
	e.FirstSeen = now
	e.LastSeen = now
	e.Reachable = true
	e.GotResponse = true
	e.InitSamples()

	idx = len(book.known)
	book.known = append(book.known, e)
	book.updateActive()

	tracef("(id=%d) \x1B[34mDiscovered path\x1B[0m %s (latency=\x1B[33m%s\x1B[0m)", book.id, e, e.latency)
}

func (book *addressBook) ReceivedHandshake(addr transports.Addr) {
	var (
		now = time.Now()
		idx = book.indexOf(addr)
		e   *addressBookEntry
	)

	if idx < 0 {
		e = &addressBookEntry{Address: addr}
		e.FirstSeen = now
		e.InitSamples()
		idx = len(book.known)
		book.known = append(book.known, e)
	} else {
		e = book.known[idx]
		e.AddLatencySample(now.Sub(e.LastAttempt))
	}

	e.LastSeen = now
	e.Reachable = true
	e.GotResponse = true

	tracef("(id=%d) \x1B[34mUpdated path\x1B[0m %s (latency=\x1B[33m%s\x1B[0m)", book.id, e, e.latency)
	book.updateActive()
}

func (book *addressBook) updateActive() {
	sort.Sort(sortedAddressBookEntries(book.known))

	if len(book.known) > c_MAX_ADDRESS_BOOK_ENTRIES {
		book.known = book.known[:c_MAX_ADDRESS_BOOK_ENTRIES]
	}

	var oldActive = book.active
	if len(book.known) > 0 && book.known[0].Reachable == true {
		book.active = book.known[0]
	} else {
		book.active = nil
	}

	if oldActive != book.active {
		tracef("(id=%d) \x1B[32mChanged path\x1B[0m from %s to %s", book.id, oldActive, book.active)
	}
}

func (book *addressBook) indexOf(addr transports.Addr) int {
	for i, e := range book.known {
		if transports.EqualAddr(e.Address, addr) {
			return i
		}
	}
	return -1
}

func (a *addressBookEntry) String() string {
	if a == nil {
		return "<nil>"
	}
	return a.Address.String()
}

func (a *addressBookEntry) AddLatencySample(d time.Duration) {
	idx := a.sample_count % 16
	a.samples[idx] = d
	a.sample_count++
	a.latency = 0
	for _, d := range a.samples {
		a.latency += d
	}
	a.latency /= 16
}

func (a *addressBookEntry) InitSamples() {
	for i := range a.samples {
		a.samples[i] = 125 * time.Millisecond
	}
	a.latency = 125 * time.Millisecond
}

type sortedAddressBookEntries []*addressBookEntry

func (s sortedAddressBookEntries) Len() int      { return len(s) }
func (s sortedAddressBookEntries) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s sortedAddressBookEntries) Less(i, j int) bool {
	if s[i].Reachable && !s[j].Reachable {
		return true
	}
	if !s[i].Reachable && s[j].Reachable {
		return false
	}

	return s[i].latency < s[j].latency
}