package tunnelmgr

import (
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/tigera/voltron/pkg/state"
	"github.com/tigera/voltron/pkg/tunnel"
)

// ErrManagerClosed is returned when a closed manager is used
var ErrManagerClosed = fmt.Errorf("manager closed")

// ErrTunnelSet is returned when the tunnel has already been set and you try to set it again with one of the SetTunnel.
var ErrTunnelSet = fmt.Errorf("tunnel already set")

// ErrStillDialing is returned when trying to open or accept a connection from the tunnel but the manager is still trying
// to open the tunnel with a dialer. This would only be returned if a dialer was set, i.e. creating a manager with
// NewManagerWithDialer.
var ErrStillDialing = fmt.Errorf("cannot access tunnel yet, still dialing")

// Manager is an interface used to manage access to tunnel(s). It synchronises access to the tunnel(s), and abstracts
// out logic necessary to interact with the tunnel(s). The main motivation for this was that both sides of the
// tunnel need to open and accept connections on a single tunnel, so instead of duplicating that logic on both the client
// and server side of the tunnel, it is abstracted out into a single component that both sides can use.
//
// [TODO] <brian mcmahon> The SetTunnel function required here may make this interface not very well defined. Currently
// [TODO] the implementation would only use SetTunnel on the "server" side of the tunnel (the side not initiating the
// [TODO] connection. We've rolled up "dialing" for the tunnel on the client side into the Manager implementation, it may
// [TODO] be a good idea to roll up "answering" that call in the Manager as well, instead of "answering" that call outside
// [TODO] of the Manager and passing the tunnel to the Manager.
type Manager interface {
	SetTunnel(t *tunnel.Tunnel) error
	Open() (net.Conn, error)
	OpenTLS(*tls.Config) (net.Conn, error)
	Listener() (net.Listener, error)
	ListenForErrors() chan error
	CloseTunnel() error
	Close() error
}

type manager struct {
	setTunnel state.SendToStateChan
	dialer    tunnel.Dialer

	openConnection   state.SendToStateChan
	addListener      state.SendToStateChan
	addErrorListener state.SendToStateChan

	closeTunnel state.SendToStateChan
	// this is used to notify the listener that the manager is closed
	close chan bool

	closeOnce sync.Once
}

// NewManager returns an instance of the Manager interface.
func NewManager() Manager {
	m := &manager{}
	m.setTunnel = make(state.SendToStateChan)

	m.openConnection = make(state.SendToStateChan)
	m.addListener = make(state.SendToStateChan)
	m.addErrorListener = make(state.SendToStateChan)
	m.closeTunnel = make(state.SendToStateChan)
	m.close = make(chan bool)

	go m.startStateLoop()
	return m
}

// NewManagerWithDialer returns an instance of the Manager interface that uses uses the given dialer to open connections
// over the tunnel.
func NewManagerWithDialer(dialer tunnel.Dialer) Manager {
	m := &manager{}
	m.dialer = dialer

	m.setTunnel = make(state.SendToStateChan)
	m.openConnection = make(state.SendToStateChan)
	m.addListener = make(state.SendToStateChan)
	m.addErrorListener = make(state.SendToStateChan)
	m.closeTunnel = make(state.SendToStateChan)
	m.close = make(chan bool)

	go m.startStateLoop()
	return m
}

// SetTunnel sets the tunnel for the manager, and returns an error if it's already running
func (m *manager) SetTunnel(t *tunnel.Tunnel) error {
	if m.isClosed() {
		return ErrManagerClosed
	}

	return state.InterfaceToError(state.Send(m.setTunnel, t))
}

// startStateLoop starts the loop to accept requests over the channels used to synchronously access the manager's state.
// Access the manager's state this way ensures we don't run into deadlocks or race conditions when a tunnel is used for
// both opening and accepting connections.
func (m *manager) startStateLoop() {
	// Dialing to the tunnel is done in a separate go routine so it doesn't block the state loop and this channel is
	// used to send the dialing result back to the state loop.
	var dialerResultsChan chan interface{}
	var dialerCloseChan chan struct{}
	defer func() {
		// If dialerCloseChan isn't nil then it's guaranteed to not be closed since the switch case that closes the channel
		// sets dialerCloseChan to nil immediately.
		if dialerCloseChan != nil {
			close(dialerCloseChan)
		}
	}()

	mClosed := false
	for !mClosed {
		log.Debug("Starting state loop.")

		ok := true
		var err error
		var tun *tunnel.Tunnel
		var setTunnel, closeTunnel, openConnection, addListener, addErrListener state.SendInterface
		var errListeners []chan error
		var tunnelErrs chan struct{}

		// [TODO] <brian mcmahon> for readability this should be changed to have the switch statement first then just break
		// [TODO] from the loop after the switch statement has executed if "ok" is false. The logic is the exact same, but
		// [TODO] I realise now that it may be confusing to see handling variables that have not yet been set.
		for ok {
			if openConnection != nil {
				err = m.handleOpenConnection(tun, openConnection, dialerResultsChan != nil)
			}
			if addListener != nil {
				err = m.handleAddListener(tun, addListener, dialerResultsChan != nil)
			}

			if err != nil {
				log.WithError(err).Debug("Handling error.")

				writeOutError(errListeners, err)
				if err == tunnel.ErrTunnelClosed {
					// If there's no dialer exit the loop to reset and wait for a new tunnel to be set.
					if m.dialer == nil {
						ok = false
						continue
					}

					// This means there's a dialer set the tunnel to nil so we trigger that block that dials for a new tunnel.
					tun = nil
				}
			}

			if tun == nil && m.dialer != nil && (dialerResultsChan == nil) {
				dialerResultsChan = make(chan interface{})
				dialerCloseChan = tunnel.DialInRoutineWithTimeout(m.dialer, dialerResultsChan, 2*time.Second)
			}

			if tun != nil {
				tunnelErrs = tun.ErrChan()
			}

			// Reset all the variables so that we don't accidentally trigger a duplication of some action on the next
			// iteration of the loop
			openConnection, addListener, addErrListener, setTunnel, err = nil, nil, nil, nil, nil
			select {
			case setTunnel, ok = <-m.setTunnel:
				log.Debug("Received request to set a new tunnel.")
				if !ok {
					continue
				}

				tun = handleSetTunnel(tun, setTunnel)
			case response := <-dialerResultsChan:
				log.Debug("Received result for dialer channel")
				close(dialerCloseChan)

				// It's the responsibility of the channel writer to close the channel, so at this point we can assume it's
				// safe to set it to nil (if it's not closed this is an error with the channel writer).
				dialerResultsChan = nil
				dialerCloseChan = nil

				switch t := response.(type) {
				case *tunnel.Tunnel:
					if tun == nil {
						tun = response.(*tunnel.Tunnel)
					} else {
						log.Warning("Tried to set tunnel from dialer when one already exists.")
						if err := response.(*tunnel.Tunnel).Close(); err != nil {
							log.WithError(err).Error("failed to close additional tunnel")
						}
					}
				case error:
					// TODO handle dialer fails as a special case as guardian may want to just crash and restart.
					err = response.(error)
				default:
					// This is a programming error, a developer wrote code that sent the wrong type over this channel
					// so fail hard.
					panic(fmt.Sprintf("unexpected type %T", t))
				}
			case openConnection, ok = <-m.openConnection:
				log.Debug("Received request open a new connection.")
			case addListener, ok = <-m.addListener:
				log.Debug("Received request for a new listener.")
			case addErrListener, ok = <-m.addErrorListener:
				log.Debug("Received request to add a new err listener.")
				if !ok {
					continue
				}

				errListener := make(chan error)
				errListeners = append(errListeners, errListener)
				addErrListener.Return(errListener)
			case closeTunnel, ok = <-m.closeTunnel:
				log.Debug("Received request to close the tunnel.")
				if !ok {
					continue
				} else if tun == nil {
					closeTunnel.Return(tunnel.ErrTunnelClosed)
				}

				closeTunnel.Close()
				ok = false
			case <-tunnelErrs:
				log.Debug("Received a tunnel error.")
				if tun != nil {
					err = tun.LastErr
				}
			case <-m.close:
				log.Debug("Received request to close the tunnel manager.")
				mClosed = true
				ok = false
			}
		}

		if openConnection != nil {
			openConnection.Return(err)
			openConnection.Close()
		}

		if addListener != nil {
			addListener.Return(err)
			addListener.Close()
		}

		for _, errorListener := range errListeners {
			close(errorListener)
		}

		if tun != nil {
			if err := tun.Close(); err != nil {
				log.WithError(err).Error("failed to close the tunnel")
			}
		}
	}
}

func writeOutError(listeners []chan error, err error) {
	for _, listener := range listeners {
		select {
		case listener <- err:
		default:
		}
	}
}

func handleSetTunnel(tun *tunnel.Tunnel, setTunnel state.SendInterface) *tunnel.Tunnel {
	defer setTunnel.Close()
	if tun != nil {
		setTunnel.Return(ErrTunnelSet)
	}

	return state.InterfaceToTunnel(setTunnel.Get())
}

// handleOpenConnection is used by the state loop to handle a request to open a connection over the tunnel
func (*manager) handleOpenConnection(tun *tunnel.Tunnel, openConnection state.SendInterface, dialing bool) error {
	log.Debug("Handling opening a connection over the tunnel.")
	if dialing {
		log.Debug("Still dialing tunnel.")
		openConnection.Return(ErrStillDialing)
		openConnection.Close()
		return nil
	}

	if tun == nil {
		log.Debug("Tunnel is nil.")
		openConnection.Return(tunnel.ErrTunnelClosed)
		openConnection.Close()
		return nil
	}

	conn, err := tun.Open()
	if err != nil {
		if err == tunnel.ErrTunnelClosed {
			log.Debug("Tunnel is closed.")
			return err
		}

		openConnection.Return(err)
	}

	tlsCfg := state.InterfaceToTLSConfig(openConnection.Get())
	if tlsCfg != nil {
		conn = tls.Client(conn, tlsCfg)
	}

	log.Debug("Connection was opened.")
	openConnection.Return(conn)
	openConnection.Close()
	return nil
}

// handleAddListener is used by the request loop to handle a request to retrieve a listener listening over the tunnel
func (m *manager) handleAddListener(tun *tunnel.Tunnel, addListener state.SendInterface, dialing bool) error {
	log.Debug("Handling add a new listener.")

	if dialing {
		log.Debug("Still dialing tunnel.")
		addListener.Return(ErrStillDialing)
		addListener.Close()
		return nil
	}

	if tun == nil {
		log.Debug("Tunnel is nil.")
		addListener.Return(tunnel.ErrTunnelClosed)
		addListener.Close()
		return nil
	}

	conResults := make(chan interface{})
	done := tun.AcceptWithChannel(conResults)
	addListener.Return(&listener{
		conns: conResults,
		done:  done,
		addr:  tun.Addr(),
		close: m.close,
	})

	return nil
}

// Open opens a connection over the tunnel
func (m *manager) Open() (net.Conn, error) {
	if m.isClosed() {
		return nil, ErrManagerClosed
	}
	return state.InterfaceToConnOrError(state.Send(m.openConnection, nil))
}

// OpenTLS opens a tls connection over the tunnel
func (m *manager) OpenTLS(cfg *tls.Config) (net.Conn, error) {
	if m.isClosed() {
		return nil, ErrManagerClosed
	}
	return state.InterfaceToConnOrError(state.Send(m.openConnection, cfg))
}

// Listener retrieves a listener listening on the tunnel for connections
func (m *manager) Listener() (net.Listener, error) {
	if m.isClosed() {
		return nil, ErrManagerClosed
	}
	return state.InterfaceToListenerOrError(state.Send(m.addListener, nil))
}

// ListenForErrors allows the user to register a channel to listen to errors on
func (m *manager) ListenForErrors() chan error {
	if m.isClosed() {
		errChan := make(chan error, 1)
		errChan <- ErrManagerClosed
		close(errChan)
		return errChan
	}
	return state.InterfaceToErrorChan(state.Send(m.addErrorListener, nil))
}

// CloseTunnel closes the managers tunnel. If a dialer is set (i.e. NewManagerWithDialer was used to create the Manager)
// then the Manager will try to re open a connection over the tunnel. If there is no dialer set (i.e. NewManager was used
// to create the Manager) then the Manager will wait for a tunnel to be set using SetTunnel.
func (m *manager) CloseTunnel() error {
	if m.isClosed() {
		return ErrManagerClosed
	}
	return state.InterfaceToError(state.Send(m.closeTunnel, true))
}

func (m *manager) isClosed() bool {
	select {
	case <-m.close:
		return true
	default:
		return false
	}
}

// Close closes the manager. A closed manager cannot be reused.
func (m *manager) Close() error {
	m.closeOnce.Do(func() {
		close(m.setTunnel)
		close(m.openConnection)
		close(m.addListener)
		close(m.addErrorListener)

		close(m.closeTunnel)
		close(m.close)
	})

	return nil
}
