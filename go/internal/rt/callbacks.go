package rt

/*
#include <metis_rt.h>
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"
)

// The C ABI calls back into Go through these two trampolines; `user` is the
// cgo.Handle of the Go function (rt.go's rt_bind_weight / rt_on_event).

//export goMetisWeight
func goMetisWeight(user unsafe.Pointer, args **C.char, nArgs C.size_t, counts *C.metis_rt_view) C.double {
	fn := cgo.Handle(uintptr(user)).Value().(WeightFunc)
	var a []string
	if nArgs > 0 {
		for _, p := range unsafe.Slice(args, nArgs) {
			a = append(a, C.GoString(p))
		}
	}
	return C.double(fn(a, View{counts}))
}

//export goMetisEvent
func goMetisEvent(user unsafe.Pointer, ev *C.metis_rt_event) {
	cgo.Handle(uintptr(user)).Value().(func(Event))(goEvent(ev))
}
