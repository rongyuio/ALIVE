package ui

import "github.com/egoist/mygo/media"

// Engine returns the engine playing the video, or nil for a player of a
// decoder's frames.
//
// alive added this on top of upstream: an app that supplies its own engine
// needs to reach the engine's own options — alive's does, to turn the video
// track off while it keeps the sound. The player stays the one to Close it.
func (p *VideoPlayer) Engine() media.Engine {
	if p.eng == nil {
		return nil
	}
	return p.eng.e
}
