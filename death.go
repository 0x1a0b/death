package death

//Manage the death of your application.

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"reflect"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Death manages the death of your application.
type Death struct {
	wg          *sync.WaitGroup
	sigChannel  chan os.Signal
	callChannel chan struct{}
	timeout     time.Duration
	log         Logger
}

// Logger interface to log.
type Logger interface {
	Error(v ...interface{})
	Debug(v ...interface{})
	Info(v ...interface{})
	Warn(v ...interface{})
}

// closer is a wrapper to the struct we are going to close with metadata
// to help with debuging close.
type closer struct {
	Index   int
	C       io.Closer
	Name    string
	PKGPath string
}

// NewDeath Create Death with the signals you want to die from.
func NewDeath(signals ...os.Signal) (death *Death) {
	death = &Death{timeout: 10 * time.Second,
		sigChannel:  make(chan os.Signal, 1),
		callChannel: make(chan struct{}, 1),
		wg:          &sync.WaitGroup{},
		log:         log.StandardLogger()}
	signal.Notify(death.sigChannel, signals...)
	death.wg.Add(1)
	go death.listenForSignal()
	return death
}

// SetTimeout Overrides the time death is willing to wait for a objects to be closed.
func (d *Death) SetTimeout(t time.Duration) *Death {
	d.timeout = t
	return d
}

// SetLogger Overrides the default logger (seelog)
func (d *Death) SetLogger(l Logger) *Death {
	d.log = l
	return d
}

// WaitForDeath wait for signal and then kill all items that need to die. If they fail to
// die when instructed we return an error
func (d *Death) WaitForDeath(closable ...io.Closer) (err error) {
	d.wg.Wait()
	d.log.Info("Shutdown started...")
	count := len(closable)
	d.log.Debug("Closing ", count, " objects")
	if count > 0 {
		return d.closeInMass(closable...)
	}
	return nil
}

// WaitForDeathWithFunc allows you to have a single function get called when it's time to
// kill your application.
func (d *Death) WaitForDeathWithFunc(f func()) {
	d.wg.Wait()
	d.log.Info("Shutdown started...")
	f()
}

// getPkgPath for an io closer.
func getPkgPath(c io.Closer) (name string, pkgPath string) {
	t := reflect.TypeOf(c)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.Name(), t.PkgPath()
}

// closeInMass Close all the objects at once and wait for them to finish with a channel. Return an
// error if you fail to close all the objects
func (d *Death) closeInMass(closable ...io.Closer) (err error) {

	count := len(closable)
	sentToClose := make(map[int]closer)
	//call close async
	doneClosers := make(chan closer, count)
	for i, c := range closable {
		name, pkgPath := getPkgPath(c)
		closer := closer{Index: i, C: c, Name: name, PKGPath: pkgPath}
		go d.closeObjects(closer, doneClosers)
		sentToClose[i] = closer
	}

	// wait on channel for notifications.
	timer := time.NewTimer(d.timeout)
	for {
		select {
		case <-timer.C:
			d.log.Warn(count, " object(s) remaining but timer expired.")
			for _, c := range sentToClose {
				d.log.Error("Failed to close: ", c.PKGPath, "/", c.Name)
			}
			return fmt.Errorf("failed to close all objects")
		case closer := <-doneClosers:
			delete(sentToClose, closer.Index)
			count--
			d.log.Debug(count, " object(s) left")
			if count == 0 && len(sentToClose) == 0 {
				d.log.Debug("Finished closing objects")
				return nil
			}
		}
	}
}

// closeObjects and return a bool when finished on a channel.
func (d *Death) closeObjects(closer closer, done chan<- closer) {
	err := closer.C.Close()
	if nil != err {
		d.log.Error(err)
	}
	done <- closer
}

// FallOnSword manually initiates the death process.
func (d *Death) FallOnSword() {
	select {
	case d.callChannel <- struct{}{}:
	default:
	}
}

// ListenForSignal Manage death of application by signal.
func (d *Death) listenForSignal() {
	defer d.wg.Done()
	for {
		select {
		case <-d.sigChannel:
			return
		case <-d.callChannel:
			return
		}
	}
}
