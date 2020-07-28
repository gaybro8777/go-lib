/*
 * Copyright (C) 2014 ~ 2018 Deepin Technology Co., Ltd.
 *
 * Author:     jouyouyun <jouyouwen717@gmail.com>
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

package dbus

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
)

const defaultSystemBusAddress = "unix:path=/var/run/dbus/system_bus_socket"

var (
	systemBus     *Conn
	systemBusLck  sync.Mutex
	sessionBus    *Conn
	sessionBusLck sync.Mutex
)

// ErrClosed is the error returned by calls on a closed connection.
var ErrClosed = errors.New("dbus: connection closed by user")

// Conn represents a connection to a message bus (usually, the system or
// session bus).
//
// Connections are either shared or private. Shared connections
// are shared between calls to the functions that return them. As a result,
// the methods Close, Auth and Hello must not be called on them.
//
// Multiple goroutines may invoke methods on a connection simultaneously.
type Conn struct {
	transport

	busObj *Object
	unixFD bool
	uuid   string

	names    []string
	namesLck sync.RWMutex

	serialLck  sync.Mutex
	nextSerial uint32
	serialUsed map[uint32]bool

	calls    map[uint32]*Call
	callsLck sync.RWMutex

	handlers    map[ObjectPath]map[string]interface{}
	handlersLck sync.RWMutex

	out    chan *Message
	closed bool
	outLck sync.RWMutex

	signals    map[<-chan *Signal]*signalChannel
	signalsLck sync.Mutex

	eavesdropped    chan<- *Message
	eavesdroppedLck sync.Mutex

	unhandledMsgs []*Message
}

// SessionBus returns a shared connection to the session bus, connecting to it
// if not already done.
func SessionBus() (conn *Conn, err error) {
	sessionBusLck.Lock()
	defer sessionBusLck.Unlock()
	if sessionBus != nil {
		return sessionBus, nil
	}
	defer func() {
		if conn != nil {
			sessionBus = conn
		}
	}()
	conn, err = SessionBusPrivate()
	if err != nil {
		return
	}
	if err = conn.Auth(nil); err != nil {
		conn.Close()
		conn = nil
		return
	}
	if err = conn.Hello(); err != nil {
		conn.Close()
		conn = nil
	}
	return
}

func getSessionBusAddress() (string, error) {
	if address := os.Getenv("DBUS_SESSION_BUS_ADDRESS"); address != "" && address != "autolaunch:" {
		return address, nil

	} else if address := tryDiscoverDbusSessionBusAddress(); address != "" {
		os.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
		return address, nil
	}
	return getSessionBusPlatformAddress()
}

// SessionBusPrivate returns a new private connection to the session bus.
func SessionBusPrivate() (*Conn, error) {
	address, err := getSessionBusAddress()
	if err != nil {
		return nil, err
	}

	return Dial(address)
}

// SystemBus returns a shared connection to the system bus, connecting to it if
// not already done.
func SystemBus() (conn *Conn, err error) {
	systemBusLck.Lock()
	defer systemBusLck.Unlock()
	if systemBus != nil {
		return systemBus, nil
	}
	defer func() {
		if conn != nil {
			systemBus = conn
		}
	}()
	conn, err = SystemBusPrivate()
	if err != nil {
		return
	}
	if err = conn.Auth(nil); err != nil {
		conn.Close()
		conn = nil
		return
	}
	if err = conn.Hello(); err != nil {
		conn.Close()
		conn = nil
	}
	return
}

// SystemBusPrivate returns a new private connection to the system bus.
func SystemBusPrivate() (*Conn, error) {
	address := os.Getenv("DBUS_SYSTEM_BUS_ADDRESS")
	if address != "" {
		return Dial(address)
	}
	return Dial(defaultSystemBusAddress)
}

// Dial establishes a new private connection to the message bus specified by address.
func Dial(address string) (*Conn, error) {
	tr, err := getTransport(address)
	if err != nil {
		return nil, err
	}
	return newConn(tr)
}

// NewConn creates a new private *Conn from an already established connection.
func NewConn(conn io.ReadWriteCloser) (*Conn, error) {
	return newConn(genericTransport{conn})
}

// newConn creates a new *Conn from a transport.
func newConn(tr transport) (*Conn, error) {
	conn := new(Conn)
	conn.transport = tr
	conn.calls = make(map[uint32]*Call)
	conn.out = make(chan *Message, 10)
	conn.handlers = make(map[ObjectPath]map[string]interface{})
	conn.nextSerial = 1
	conn.serialUsed = map[uint32]bool{0: true}
	conn.busObj = conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus")
	conn.signals = make(map[<-chan *Signal]*signalChannel)
	return conn, nil
}

// BusObject returns the object owned by the bus daemon which handles
// administrative requests.
func (conn *Conn) BusObject() *Object {
	return conn.busObj
}

// Close closes the connection. Any blocked operations will return with errors
// and the channels passed to Eavesdrop and Signal are closed. This method must
// not be called on shared connections.
func (conn *Conn) Close() error {
	conn.outLck.Lock()
	close(conn.out)
	conn.closed = true
	conn.outLck.Unlock()
	conn.signalsLck.Lock()
	for _, ch := range conn.signals {
		ch.Close()
	}
	conn.signalsLck.Unlock()
	conn.eavesdroppedLck.Lock()
	if conn.eavesdropped != nil {
		close(conn.eavesdropped)
	}
	conn.eavesdroppedLck.Unlock()
	e := conn.transport.Close()
	quit(e)
	return e
}

// Eavesdrop causes conn to send all incoming messages to the given channel
// without further processing. Method replies, errors and signals will not be
// sent to the appropiate channels and method calls will not be handled. If nil
// is passed, the normal behaviour is restored.
//
// The caller has to make sure that ch is sufficiently buffered;
// if a message arrives when a write to ch is not possible, the message is
// discarded.
func (conn *Conn) Eavesdrop(ch chan<- *Message) {
	conn.eavesdroppedLck.Lock()
	conn.eavesdropped = ch
	conn.eavesdroppedLck.Unlock()
}

// getSerial returns an unused serial.
func (conn *Conn) getSerial() uint32 {
	conn.serialLck.Lock()
	defer conn.serialLck.Unlock()
	n := conn.nextSerial
	for conn.serialUsed[n] {
		n++
	}
	conn.serialUsed[n] = true
	conn.nextSerial = n + 1
	return n
}

// Hello sends the initial org.freedesktop.DBus.Hello call. This method must be
// called after authentication, but before sending any other messages to the
// bus. Hello must not be called for shared connections.
func (conn *Conn) Hello() error {
	var s string
	err := conn.busObj.Call("org.freedesktop.DBus.Hello", 0).Store(&s)
	if err != nil {
		return err
	}
	conn.namesLck.Lock()
	conn.names = make([]string, 1)
	conn.names[0] = s
	conn.namesLck.Unlock()
	return nil
}

// inWorker runs in an own goroutine, reading incoming messages from the
// transport and dispatching them appropiately.
func (conn *Conn) inWorker() {
	for {
		msg, err := conn.ReadMessage()
		if err == nil {
			conn.eavesdroppedLck.Lock()
			if conn.eavesdropped != nil {
				select {
				case conn.eavesdropped <- msg:
				default:
				}
				conn.eavesdroppedLck.Unlock()
				continue
			}
			conn.eavesdroppedLck.Unlock()
			dest, _ := msg.Headers[FieldDestination].value.(string)
			found := false
			if dest == "" {
				found = true
			} else {
				conn.namesLck.RLock()
				if len(conn.names) == 0 {
					found = true
				}
				for _, v := range conn.names {
					if dest == v {
						found = true
						break
					}
				}
				conn.namesLck.RUnlock()
			}
			if IS_LAUNCHED_BY_BUS_DAEMON && !found && msg.Type == TypeMethodCall && !strings.Contains(dest, ":") {
				conn.unhandledMsgs = append(conn.unhandledMsgs, msg)
			}
			if !found {
				// Eavesdropped a message, but no channel for it is registered.
				// Ignore it.
				continue
			}
			switch msg.Type {
			case TypeMethodReply, TypeError:
				serial := msg.Headers[FieldReplySerial].value.(uint32)
				conn.callsLck.Lock()
				if c, ok := conn.calls[serial]; ok {
					if msg.Type == TypeError {
						name, _ := msg.Headers[FieldErrorName].value.(string)
						c.Err = dbusError{name, msg.Body}
					} else {
						c.Body = msg.Body
					}
					c.Done <- c
					conn.serialLck.Lock()
					delete(conn.serialUsed, serial)
					conn.serialLck.Unlock()
					delete(conn.calls, serial)
				}
				conn.callsLck.Unlock()
			case TypeSignal:
				iface := msg.Headers[FieldInterface].value.(string)
				member := msg.Headers[FieldMember].value.(string)
				// as per http://dbus.freedesktop.org/doc/dbus-specification.html ,
				// sender is optional for signals.
				sender, _ := msg.Headers[FieldSender].value.(string)
				if iface == "org.freedesktop.DBus" && member == "NameLost" &&
					sender == "org.freedesktop.DBus" {

					name, _ := msg.Body[0].(string)
					conn.namesLck.Lock()
					for i, v := range conn.names {
						if v == name {
							copy(conn.names[i:], conn.names[i+1:])
							conn.names = conn.names[:len(conn.names)-1]
						}
					}
					conn.namesLck.Unlock()
				}
				signal := &Signal{
					Sender: sender,
					Path:   msg.Headers[FieldPath].value.(ObjectPath),
					Name:   iface + "." + member,
					Body:   msg.Body,
				}

				conn.sendSignals(signal)
			case TypeMethodCall:
				go conn.handleCall(msg)
			}
		} else if _, ok := err.(InvalidMessageError); !ok {
			// Some read error occured (usually EOF); we can't really do
			// anything but to shut down all stuff and returns errors to all
			// pending replies.
			conn.Close()
			conn.callsLck.RLock()
			for _, v := range conn.calls {
				v.Err = err
				v.Done <- v
			}
			conn.callsLck.RUnlock()
			return
		}
		// invalid messages are ignored
	}
}

// Names returns the list of all names that are currently owned by this
// connection. The slice is always at least one element long, the first element
// being the unique name of the connection.
func (conn *Conn) Names() []string {
	conn.namesLck.RLock()
	// copy the slice so it can't be modified
	s := make([]string, len(conn.names))
	copy(s, conn.names)
	conn.namesLck.RUnlock()
	return s
}

// Object returns the object identified by the given destination name and path.
func (conn *Conn) Object(dest string, path ObjectPath) *Object {
	return &Object{conn, dest, path}
}

// outWorker runs in an own goroutine, encoding and sending messages that are
// sent to conn.out.
func (conn *Conn) outWorker() {
	for msg := range conn.out {
		err := conn.SendMessage(msg)
		conn.callsLck.RLock()
		if err != nil {
			if c := conn.calls[msg.serial]; c != nil {
				c.Err = err
				c.Done <- c
			}
			conn.serialLck.Lock()
			delete(conn.serialUsed, msg.serial)
			conn.serialLck.Unlock()
		} else if msg.Type != TypeMethodCall {
			conn.serialLck.Lock()
			delete(conn.serialUsed, msg.serial)
			conn.serialLck.Unlock()
		}
		conn.callsLck.RUnlock()
	}
}

// Send sends the given message to the message bus. You usually don't need to
// use this; use the higher-level equivalents (Call / Go, Emit and Export)
// instead. If msg is a method call and NoReplyExpected is not set, a non-nil
// call is returned and the same value is sent to ch (which must be buffered)
// once the call is complete. Otherwise, ch is ignored and a Call structure is
// returned of which only the Err member is valid.
func (conn *Conn) Send(msg *Message, ch chan *Call) *Call {
	var call *Call

	msg.serial = conn.getSerial()
	if msg.Type == TypeMethodCall && msg.Flags&FlagNoReplyExpected == 0 {
		if ch == nil {
			ch = make(chan *Call, 5)
		} else if cap(ch) == 0 {
			panic("dbus: unbuffered channel passed to (*Conn).Send")
		}
		call = new(Call)
		call.Destination, _ = msg.Headers[FieldDestination].value.(string)
		call.Path, _ = msg.Headers[FieldPath].value.(ObjectPath)
		iface, _ := msg.Headers[FieldInterface].value.(string)
		member, _ := msg.Headers[FieldMember].value.(string)
		call.Method = iface + "." + member
		call.Args = msg.Body
		call.Done = ch
		conn.callsLck.Lock()
		conn.calls[msg.serial] = call
		conn.callsLck.Unlock()
		conn.outLck.RLock()
		if conn.closed {
			call.Err = ErrClosed
			call.Done <- call
		} else {
			conn.out <- msg
		}
		conn.outLck.RUnlock()
	} else {
		conn.outLck.RLock()
		if conn.closed {
			call = &Call{Err: ErrClosed}
		} else {
			conn.out <- msg
			call = &Call{Err: nil}
		}
		conn.outLck.RUnlock()
	}
	return call
}

// sendError creates an error message corresponding to the parameters and sends
// it to conn.out.
func (conn *Conn) sendError(ee error, dest string, serial uint32) {
	var e dbusError
	var ok bool
	if e, ok = ee.(dbusError); !ok {
		e = NewOtherError(ee)
	}

	msg := new(Message)
	msg.Type = TypeError
	msg.serial = conn.getSerial()
	msg.Headers = make(map[HeaderField]Variant)
	msg.Headers[FieldDestination] = MakeVariant(dest)
	msg.Headers[FieldErrorName] = MakeVariant(e.Name)
	msg.Headers[FieldReplySerial] = MakeVariant(serial)
	msg.Body = e.Body
	if len(e.Body) > 0 {
		msg.Headers[FieldSignature] = MakeVariant(SignatureOf(e.Body...))
	}
	conn.outLck.RLock()
	if !conn.closed {
		conn.out <- msg
	}
	conn.outLck.RUnlock()
}

// sendReply creates a method reply message corresponding to the parameters and
// sends it to conn.out.
func (conn *Conn) sendReply(dest string, serial uint32, values ...interface{}) {
	msg := new(Message)
	msg.Type = TypeMethodReply
	msg.serial = conn.getSerial()
	msg.Headers = make(map[HeaderField]Variant)
	msg.Headers[FieldDestination] = MakeVariant(dest)
	msg.Headers[FieldReplySerial] = MakeVariant(serial)
	msg.Body = values
	if len(values) > 0 {
		msg.Headers[FieldSignature] = MakeVariant(SignatureOf(values...))
	}
	conn.outLck.RLock()
	if !conn.closed {
		conn.out <- msg
	}
	conn.outLck.RUnlock()
}

// SupportsUnixFDs returns whether the underlying transport supports passing of
// unix file descriptors. If this is false, method calls containing unix file
// descriptors will return an error and emitted signals containing them will
// not be sent.
func (conn *Conn) SupportsUnixFDs() bool {
	return conn.unixFD
}

// Signal represents a D-Bus message of type Signal. The name member is given in
// "interface.member" notation, e.g. org.freedesktop.D-Bus.NameLost.
type Signal struct {
	Sender string
	Path   ObjectPath
	Name   string
	Body   []interface{}
}

// transport is a D-Bus transport.
type transport interface {
	// Read and Write raw data (for example, for the authentication protocol).
	io.ReadWriteCloser

	// Send the initial null byte used for the EXTERNAL mechanism.
	SendNullByte() error

	// Returns whether this transport supports passing Unix FDs.
	SupportsUnixFDs() bool

	// Signal the transport that Unix FD passing is enabled for this connection.
	EnableUnixFDs()

	// Read / send a message, handling things like Unix FDs.
	ReadMessage() (*Message, error)
	SendMessage(*Message) error
}

func getTransport(address string) (transport, error) {
	var err error
	var t transport

	m := map[string]func(string) (transport, error){
		"unix": newUnixTransport,
	}
	addresses := strings.Split(address, ";")
	for _, v := range addresses {
		i := strings.IndexRune(v, ':')
		if i == -1 {
			err = errors.New("dbus: invalid bus address (no transport)")
			continue
		}
		f := m[v[:i]]
		if f == nil {
			_ = errors.New("dbus: invalid bus address (invalid or unsupported transport)")
		}
		t, err = f(v[i+1:])
		if err == nil {
			return t, nil
		}
	}
	return nil, err
}

// getKey gets a key from a the list of keys. Returns "" on error / not found...
func getKey(s, key string) string {
	i := strings.Index(s, key)
	if i == -1 {
		return ""
	}
	if i+len(key)+1 >= len(s) || s[i+len(key)] != '=' {
		return ""
	}
	j := strings.Index(s, ",")
	if j == -1 {
		j = len(s)
	}
	return s[i+len(key)+1 : j]
}

var IS_LAUNCHED_BY_BUS_DAEMON bool = false

func init() {
	bus := os.Getenv("DBUS_STARTER_BUS_TYPE")
	if bus == "session" || bus == "system" {
		IS_LAUNCHED_BY_BUS_DAEMON = true
	}
}

func DealWithUnhandledMessage() {
	switch os.Getenv("DBUS_STARTER_BUS_TYPE") {
	case "session":
		if conn, err := SessionBus(); err == nil {
			for _, msg := range conn.unhandledMsgs {
				go conn.handleCall(msg)
			}
			conn.unhandledMsgs = nil
		}
	case "system":
		if conn, err := SystemBus(); err == nil {
			for _, msg := range conn.unhandledMsgs {
				go conn.handleCall(msg)
			}
			conn.unhandledMsgs = nil
		}
	}
}
