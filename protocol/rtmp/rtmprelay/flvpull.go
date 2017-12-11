package rtmprelay

import (
	"errors"
	"fmt"
	"github.com/livego/av"
	log "github.com/livego/logging"
	"github.com/livego/protocol/httpflvclient"
	"github.com/livego/protocol/rtmp/core"
	"time"
)

type FlvPull struct {
	FlvUrl        string
	RtmpUrl       string
	flvclient     *httpflvclient.HttpFlvClient
	rtmpclient    *core.ConnClient
	isStart       bool
	isRtmpConnect bool
	csChan        chan *core.ChunkStream
	flvErrChan    chan int
	rtmpErrChan   chan int
	isFlvHdrReady bool
	databuffer    []byte
	dataNeedLen   int
	testFlag      bool
	firstVideo    bool
	firstAudio    bool
	videoHdr      *core.ChunkStream
	audioHdr      *core.ChunkStream
}

const FLV_HEADER_LENGTH = 13

func NewFlvPull(flvurl *string, rtmpurl *string) *FlvPull {
	return &FlvPull{
		FlvUrl:      *flvurl,
		RtmpUrl:     *rtmpurl,
		isStart:     false,
		csChan:      make(chan *core.ChunkStream, 1000),
		flvErrChan:  make(chan int),
		rtmpErrChan: make(chan int),
		firstVideo:  false,
		firstAudio:  false,
		videoHdr:    nil,
		audioHdr:    nil,
	}
}

func (self *FlvPull) StatusReport(stat int) {
	defer func() {
		if e := recover(); e != nil {
			log.Errorf("channel has already been closed:%v", e)
		}
	}()
	if stat == httpflvclient.FLV_ERROR {
		self.flvErrChan <- 1
	} else if stat == httpflvclient.RTMP_ERROR {
		self.rtmpErrChan <- 1
	}
}

func (self *FlvPull) HandleFlvData(packet []byte, srcUrl string) error {
	defer func() {
		if e := recover(); e != nil {
			log.Errorf("HandleFlvData cs channel has already been closed:%v", e)
			return
		}
	}()
	var cs *core.ChunkStream

	cs = &core.ChunkStream{}
	messagetype := packet[0]
	payloadLen := int(packet[1])<<16 + int(packet[2])<<8 + int(packet[3])
	timestamp := int(packet[4])<<16 + int(packet[5])<<8 + int(packet[6]) + int(packet[7])<<24
	streamid := int(packet[8])<<16 + int(packet[9])<<8 + int(packet[10])

	if messagetype == 0x09 {
		/*
			if packet[11] == 0x17 && packet[12] == 0x00 {
				//log.Printf("it's pps and sps: messagetype=%d, payloadlen=%d, timestamp=%d, streamid=%d", messagetype, payloadLen, timestamp, streamid)
				cs.TypeID = av.TAG_VIDEO
			} else if packet[11] == 0x17 && packet[12] == 0x01 {
				//log.Printf("it's I frame: messagetype=%d, payloadlen=%d, timestamp=%d, streamid=%d", messagetype, payloadLen, timestamp, streamid)
				cs.TypeID = av.TAG_VIDEO
			} else if packet[11] == 0x27 {
				cs.TypeID = av.TAG_VIDEO
				//log.Printf("it's P frame: messagetype=%d, payloadlen=%d, timestamp=%d, streamid=%d", messagetype, payloadLen, timestamp, streamid)
			}
		*/
		cs.TypeID = av.TAG_VIDEO
	} else if messagetype == 0x08 {
		cs.TypeID = av.TAG_AUDIO
		//log.Printf("it's audio: messagetype=%d, payloadlen=%d, timestamp=%d, streamid=%d", messagetype, payloadLen, timestamp, streamid)
	} else if messagetype == 0x12 {
		cs.TypeID = av.MetadatAMF0
		//log.Printf("it's metadata: messagetype=%d, payloadlen=%d, timestamp=%d, streamid=%d", messagetype, payloadLen, timestamp, streamid)
	} else if messagetype == 0xff {
		cs.TypeID = av.MetadataAMF3
	} else {
		cs.TypeID = uint32(messagetype)
	}

	cs.Data = packet[11:]
	cs.Length = uint32(payloadLen)
	cs.StreamID = uint32(streamid)
	cs.Timestamp = uint32(timestamp)

	if uint32(payloadLen) != cs.Length {
		errString := fmt.Sprintf("payload length(%d) is not equal to data length(%d)",
			payloadLen, cs.Length)
		return errors.New(errString)
	}
	//log.Infof("++++-->csChan type=%d, length=%d, timestamp=%d, messagetype=0x%02x, packet=%02x %02x",
	//	cs.TypeID, cs.Length, cs.Timestamp, messagetype, packet[11], packet[12])
	self.csChan <- cs
	return nil
}

func (self *FlvPull) clean() {
	self.videoHdr = nil
	self.audioHdr = nil
	self.firstVideo = false
	self.firstAudio = false
}

func (self *FlvPull) setHeader(csPacket *core.ChunkStream) {
	if csPacket != nil {
		if (csPacket.Data[0] == 0x17) && (csPacket.Data[1] == 0x00) {
			self.videoHdr = csPacket
		} else if (csPacket.Data[0] == 0xaf) && (csPacket.Data[1] == 0x00) {
			self.audioHdr = csPacket
		}
	}
}

func (self *FlvPull) sendPublishChunkStream() {
	var err error
	for {
		select {
		case csPacket, ok := <-self.csChan:
			if ok {
				if !self.isRtmpConnect {
					continue
				}
				self.setHeader(csPacket)

				err = self.rtmpclient.Write(*csPacket)
				if err != nil {
					log.Errorf("On_running type=%d, length=%d, timestamp=%d, error=%v",
						csPacket.TypeID, csPacket.Length, csPacket.Timestamp, err)
					self.isRtmpConnect = false
					self.rtmpclient.Close(nil)
					self.rtmpclient = nil
					self.firstVideo = false
					self.firstAudio = false
					go self.StatusReport(httpflvclient.RTMP_ERROR)
					log.Infof("insert rtmpErrChan")
				} else {
					//log.Infof("###sendPublishChunkStream ok local type=%d, length=%d, timestamp=%d.",
					//	csPacket.TypeID, csPacket.Length, csPacket.Timestamp)
					if !self.firstVideo && self.videoHdr != nil && self.videoHdr != csPacket {
						log.Warningf("first video Header:%02x %02x", self.videoHdr.Data[0], self.videoHdr.Data[1])
						err = self.rtmpclient.Write(*self.videoHdr)
						if err == nil {
							self.firstVideo = true
						}
					}
					if !self.firstAudio && self.audioHdr != nil && self.audioHdr != csPacket {
						log.Warningf("first audio Header:%02x %02x", self.audioHdr.Data[0], self.audioHdr.Data[1])
						err = self.rtmpclient.Write(*self.audioHdr)
						if err == nil {
							self.firstAudio = true
						}
					}
				}
			} else {
				break
			}
		case _, ok := <-self.flvErrChan:
			if ok {
				log.Info("flvErrChan get message....")
				self.flvclient.Stop()
				self.clean()

				err := self.flvclient.Start(self)
				if err != nil {
					log.Errorf("On_running flvclient start error:%v", err)
					time.Sleep(2 * time.Second)
					go self.StatusReport(httpflvclient.FLV_ERROR)
				}
				self.isRtmpConnect = false
				if self.rtmpclient != nil {
					self.rtmpclient.Close(nil)
				}
				self.rtmpclient = core.NewConnClient()
				err = self.rtmpclient.Start(self.RtmpUrl, "publish")
				if err != nil {
					log.Errorf("On_running rtmpclient.Start url=%v error=%v", self.RtmpUrl, err)
					go self.StatusReport(httpflvclient.RTMP_ERROR)
					log.Infof("rtmpclient.Start error: insert rtmpErrChan")
				} else {
					self.isRtmpConnect = true
				}
			} else {
				break
			}
		case _, ok := <-self.rtmpErrChan:
			if ok {
				log.Info("rtmpErrChan get message....")
				time.Sleep(3 * time.Second)
				self.rtmpclient = core.NewConnClient()
				err := self.rtmpclient.Start(self.RtmpUrl, "publish")
				if err != nil {
					log.Errorf("On_running rtmpclient.Start url=%v error=%v", self.RtmpUrl, err)
					go self.StatusReport(httpflvclient.RTMP_ERROR)
					log.Infof("rtmpclient.Start error: insert rtmpErrChan")
				} else {
					self.isRtmpConnect = true
				}
			} else {
				break
			}
		}
	}

	log.Info("sendPublishChunkStream is ended.")
}

func (self *FlvPull) Start() error {
	if self.isStart {
		errString := fmt.Sprintf("FlvPull(%s->%s) has already started.", self.FlvUrl, self.RtmpUrl)
		return errors.New(errString)
	}
	self.flvclient = httpflvclient.NewHttpFlvClient(self.FlvUrl)
	if self.flvclient == nil {
		errString := fmt.Sprintf("FlvPull(%s) error", self.FlvUrl)
		return errors.New(errString)
	}

	self.rtmpclient = core.NewConnClient()

	self.csChan = make(chan *core.ChunkStream)

	self.isFlvHdrReady = false
	self.databuffer = nil

	self.clean()
	err := self.flvclient.Start(self)
	if err != nil {
		log.Errorf("flvclient start error:%v", err)
		close(self.csChan)
		return err
	}

	err = self.rtmpclient.Start(self.RtmpUrl, "publish")
	if err != nil {
		log.Errorf("rtmpclient.Start url=%v error", self.RtmpUrl)

		self.flvclient.Stop()
		self.clean()
		close(self.csChan)
		return err
	}

	self.isRtmpConnect = true
	self.isStart = true

	go self.sendPublishChunkStream()

	return nil
}

func (self *FlvPull) Stop() {
	if !self.isStart {
		log.Errorf("FlvPull(%s->%s) has already stoped.", self.FlvUrl, self.RtmpUrl)
		return
	}
	self.isRtmpConnect = false
	self.isStart = false

	self.flvclient.Stop()
	self.clean()
	self.rtmpclient.Close(nil)

	close(self.csChan)
	close(self.flvErrChan)
	close(self.rtmpErrChan)
	log.Infof("FlvPull(%s->%s) stoped.", self.FlvUrl, self.RtmpUrl)
}

func (self *FlvPull) IsStart() bool {
	return self.isStart
}
