// Package transport wraps pion/webrtc for the RDP Link media + data path.
package transport

import (
	"fmt"

	"github.com/pion/webrtc/v4"
)

// Config holds ICE/STUN/TURN configuration.
type Config struct {
	STUNServers []string
	TURNServers []webrtc.ICEServer
}

// Peer is a WebRTC peer connection wrapper.
type Peer struct {
	PC     *webrtc.PeerConnection
	Config Config
}

// NewPeer creates a new peer connection with the given ICE config.
func NewPeer(cfg Config) (*Peer, error) {
	iceServers := []webrtc.ICEServer{}
	for _, s := range cfg.STUNServers {
		iceServers = append(iceServers, webrtc.ICEServer{URLs: []string{s}})
	}
	iceServers = append(iceServers, cfg.TURNServers...)
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: iceServers,
	})
	if err != nil {
		return nil, err
	}
	return &Peer{PC: pc, Config: cfg}, nil
}

// AddDataChannel creates a reliable ordered data channel.
func (p *Peer) AddDataChannel(label string) (*webrtc.DataChannel, error) {
	if p.PC == nil {
		return nil, fmt.Errorf("nil peer connection")
	}
	ordered := true
	return p.PC.CreateDataChannel(label, &webrtc.DataChannelInit{Ordered: &ordered})
}

// CreateOffer generates an SDP offer.
func (p *Peer) CreateOffer() (webrtc.SessionDescription, error) {
	offer, err := p.PC.CreateOffer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	if err := p.PC.SetLocalDescription(offer); err != nil {
		return webrtc.SessionDescription{}, err
	}
	return offer, nil
}

// AcceptOffer sets remote offer and creates answer.
func (p *Peer) AcceptOffer(offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	if err := p.PC.SetRemoteDescription(offer); err != nil {
		return webrtc.SessionDescription{}, err
	}
	answer, err := p.PC.CreateAnswer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}
	if err := p.PC.SetLocalDescription(answer); err != nil {
		return webrtc.SessionDescription{}, err
	}
	return answer, nil
}

// SetRemoteAnswer sets the remote answer.
func (p *Peer) SetRemoteAnswer(answer webrtc.SessionDescription) error {
	return p.PC.SetRemoteDescription(answer)
}

// AddICECandidate adds a trickle ICE candidate.
func (p *Peer) AddICECandidate(c webrtc.ICECandidateInit) error {
	return p.PC.AddICECandidate(c)
}

// Close closes the peer connection.
func (p *Peer) Close() error {
	if p.PC == nil {
		return nil
	}
	return p.PC.Close()
}
