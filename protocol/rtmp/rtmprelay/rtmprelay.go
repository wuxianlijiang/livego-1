package rtmprelay

import (
	"bytes"
	"errors"
	"fmt"
	log "github.com/livego/logging"
	"github.com/livego/protocol/amf"
	"github.com/livego/protocol/rtmp/core"
	"io"
	"time"
)

var (
	STOP_CTRL = "RTMPRELAY_STOP"
)

type RtmpRelay struct {
	PlayUrl              string
	PublishUrl           string
	cs_chan              chan *core.ChunkStream
	sndctrl_chan         chan string
	connectPlayClient    *core.ConnClient
	connectPublishClient *core.ConnClient
	startflag            bool
	pushReconnectFlag    bool
	pullReconnectFlag    bool
	firstVideo           bool
	firstAudio           bool
	videoHdr             *core.ChunkStream
	audioHdr             *core.ChunkStream
}

func NewRtmpRelay(playurl *string, publishurl *string) *RtmpRelay {
	return &RtmpRelay{
		PlayUrl:              *playurl,
		PublishUrl:           *publishurl,
		cs_chan:              make(chan *core.ChunkStream, 500),
		sndctrl_chan:         make(chan string),
		connectPlayClient:    nil,
		connectPublishClient: nil,
		startflag:            false,
	}
}

func (self *RtmpRelay) clean() {
	self.videoHdr = nil
	self.audioHdr = nil
	self.firstVideo = false
	self.firstAudio = false
}

func (self *RtmpRelay) setHeader(csPacket *core.ChunkStream) {
	if csPacket != nil {
		if (csPacket.Data[0] == 0x17) && (csPacket.Data[1] == 0x00) {
			self.videoHdr = csPacket
		} else if (csPacket.Data[0] == 0xaf) && (csPacket.Data[1] == 0x00) {
			self.audioHdr = csPacket
		}
	}
}

func (self *RtmpRelay) reconnectPull() error {
	self.connectPlayClient.Close(nil)

	self.connectPlayClient = nil
	self.connectPlayClient = core.NewConnClient()
	err := self.connectPlayClient.Start(self.PlayUrl, "play")
	if err != nil {
		log.Errorf("reconnectPull connectPlayClient.Start url=%v error", self.PlayUrl)
		self.pullReconnectFlag = true
		return err
	}
	self.pullReconnectFlag = false

	return nil
}

func (self *RtmpRelay) rcvPlayChunkStream() {
	log.Info("rcvPlayRtmpMediaPacket connectClient.Read...")
	tryCount := 0
	lasttimestamp := time.Now().Unix()

	defer func() {
		if e := recover(); e != nil {
			log.Errorf("rcvPlayChunkStream cs channel has already been closed:%v", e)
			return
		}
	}()
	for {
		rc := &core.ChunkStream{}

		if self.startflag == false {
			self.connectPlayClient.Close(nil)
			log.Infof("rcvPlayChunkStream close: playurl=%s, publishurl=%s", self.PlayUrl, self.PublishUrl)
			break
		}
		if !self.pullReconnectFlag {
			err := self.connectPlayClient.Read(rc)
			if err != nil {
				tryCount++
			}
			if (err != nil && err == io.EOF) || tryCount > 3 {
				err = self.reconnectPull()
				if err != nil {
					time.Sleep(time.Second * 2)
					continue
				}
			}
			if err != nil {
				continue
			}
			tryCount = 0

			switch rc.TypeID {
			case 20, 17:
				r := bytes.NewReader(rc.Data)
				vs, err := self.connectPlayClient.DecodeBatch(r, amf.AMF0)

				log.Infof("rcvPlayRtmpMediaPacket: vs=%v, err=%v", vs, err)
			case 18, 8, 9:
				if rc.TypeID == 18 {
					log.Infof("rcvPlayRtmpMediaPacket: metadata....")
				}
				//log.Infof("rcvPlayChunkStream:typeid=%d, length=%d, timestamp=%d",
				//	rc.TypeID, rc.Length, rc.Timestamp)
				self.cs_chan <- rc
			}
		} else {
			tryCount = 0
			nowTime := time.Now().Unix()
			if nowTime-lasttimestamp >= 2 {
				self.reconnectPull()
				lasttimestamp = time.Now().Unix()
			}
		}
	}
}

func (self *RtmpRelay) reconnectPush() error {
	self.connectPublishClient.Close(nil)
	self.connectPublishClient = nil

	self.connectPublishClient = core.NewConnClient()
	log.Infof("reconnectPush server addr:%v starting....", self.PublishUrl)
	err := self.connectPublishClient.Start(self.PublishUrl, "publish")
	if err != nil {
		log.Errorf("reconnectPush connectPublishClient.Start url=%v error", self.PublishUrl)
		self.pushReconnectFlag = true
		return err
	}
	self.pushReconnectFlag = false
	return nil
}
func (self *RtmpRelay) sendPublishChunkStream() {
	lasttimestamp := time.Now().Unix()
	for {
		select {
		case rc := <-self.cs_chan:
			//log.Infof("sendPublishChunkStream: rc.TypeID=%v length=%d, pushReconnectFlag=%v",
			//	rc.TypeID, len(rc.Data), self.pushReconnectFlag)
			if !self.pushReconnectFlag {
				self.setHeader(rc)
				err := self.connectPublishClient.Write(*rc)
				if err != nil {
					self.firstVideo = false
					self.firstAudio = false
					self.reconnectPush()
					lasttimestamp = time.Now().Unix()
				} else {
					//log.Infof("###sendPublishChunkStream ok local type=%d, length=%d, timestamp=%d.",
					//	csPacket.TypeID, csPacket.Length, csPacket.Timestamp)
					if !self.firstVideo && self.videoHdr != nil && self.videoHdr != rc {
						log.Warningf("rtmprelay first video Header:%02x %02x", self.videoHdr.Data[0], self.videoHdr.Data[1])
						err = self.connectPublishClient.Write(*self.videoHdr)
						if err == nil {
							self.firstVideo = true
						}
					}
					if !self.firstAudio && self.audioHdr != nil && self.audioHdr != rc {
						log.Warningf("rtmprelay first audio Header:%02x %02x", self.audioHdr.Data[0], self.audioHdr.Data[1])
						err = self.connectPublishClient.Write(*self.audioHdr)
						if err == nil {
							self.firstAudio = true
						}
					}
				}
			} else {
				nowTime := time.Now().Unix()
				if nowTime-lasttimestamp >= 2 {
					err := self.reconnectPush()
					lasttimestamp = time.Now().Unix()
					if err == nil {
						self.connectPublishClient.Write(*rc)
					}
				}
			}
		case ctrlcmd := <-self.sndctrl_chan:
			if ctrlcmd == STOP_CTRL {
				self.connectPublishClient.Close(nil)
				log.Infof("sendPublishChunkStream close: playurl=%s, publishurl=%s", self.PlayUrl, self.PublishUrl)
				break
			}
		}
	}
}

func (self *RtmpRelay) IsStart() bool {
	return self.startflag
}

func (self *RtmpRelay) Start() error {
	if self.startflag {
		err := errors.New(fmt.Sprintf("The rtmprelay already started, playurl=%s, publishurl=%s", self.PlayUrl, self.PublishUrl))
		return err
	}

	self.connectPlayClient = core.NewConnClient()
	self.connectPublishClient = core.NewConnClient()

	log.Infof("play server addr:%v starting....", self.PlayUrl)
	err := self.connectPlayClient.Start(self.PlayUrl, "play")
	if err != nil {
		log.Errorf("connectPlayClient.Start url=%v error", self.PlayUrl)
		return err
	}

	log.Infof("publish server addr:%v starting....", self.PublishUrl)
	err = self.connectPublishClient.Start(self.PublishUrl, "publish")
	if err != nil {
		log.Errorf("connectPublishClient.Start url=%v error", self.PublishUrl)
		self.connectPlayClient.Close(nil)
		return err
	}

	self.startflag = true
	self.pullReconnectFlag = false
	self.pushReconnectFlag = false
	go self.rcvPlayChunkStream()
	go self.sendPublishChunkStream()

	return nil
}

func (self *RtmpRelay) Stop() {
	if !self.startflag {
		log.Errorf("The rtmprelay already stoped, playurl=%s, publishurl=%s", self.PlayUrl, self.PublishUrl)
		return
	}

	self.startflag = false
	self.sndctrl_chan <- STOP_CTRL

	self.connectPlayClient.Close(nil)
	self.connectPublishClient.Close(nil)
}
