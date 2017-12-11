package core

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	neturl "net/url"
	"strings"

	"github.com/livego/av"
	log "github.com/livego/logging"
	"github.com/livego/protocol/amf"
)

var (
	respResult     = "_result"
	respError      = "_error"
	onStatus       = "onStatus"
	publishStart   = "NetStream.Publish.Start"
	playStart      = "NetStream.Play.Start"
	connectSuccess = "NetConnection.Connect.Success"
	onBWDone       = "onBWDone"
)

var (
	ErrFail = errors.New("respone err")
)

type ConnClient struct {
	done          bool
	transID       int
	url           string
	tcurl         string
	app           string
	title         string
	query         string
	curcmdName    string
	streamid      uint32
	suburl        [2]string
	subtcurl      [2]string
	subapp        [2]string
	subtitle      [2]string
	subquery      [2]string
	subcurcmdName [2]string
	substreamid   [2]uint32
	conn          *Conn
	encoder       *amf.Encoder
	decoder       *amf.Decoder
	bytesw        *bytes.Buffer
	IsStartFlag   bool
}

func NewConnClient() *ConnClient {
	return &ConnClient{
		transID: 1,
		bytesw:  bytes.NewBuffer(nil),
		encoder: &amf.Encoder{},
		decoder: &amf.Decoder{},
	}
}

func (self *ConnClient) GetUrl() string {
	return self.url
}

func (connClient *ConnClient) DecodeBatch(r io.Reader, ver amf.Version) (ret []interface{}, err error) {
	vs, err := connClient.decoder.DecodeBatch(r, ver)
	return vs, err
}

func (connClient *ConnClient) readRespMsg() error {
	var err error
	var rc ChunkStream
	for {
		if err = connClient.conn.Read(&rc); err != nil {
			return err
		}
		if err != nil && err != io.EOF {
			return err
		}
		switch rc.TypeID {
		case 20, 17:
			r := bytes.NewReader(rc.Data)
			vs, _ := connClient.decoder.DecodeBatch(r, amf.AMF0)

			log.Infof("readRespMsg: vs=%v", vs)
			for k, v := range vs {
				switch v.(type) {
				case string:
					switch connClient.curcmdName {
					case cmdConnect, cmdCreateStream:
						if v.(string) != respResult {
							return errors.New(v.(string))
						}

					case cmdPublish:
						if v.(string) != onStatus {
							return ErrFail
						}
					}
				case float64:
					switch connClient.curcmdName {
					case cmdConnect, cmdCreateStream:
						id := int(v.(float64))

						if k == 1 {
							if id != connClient.transID {
								return ErrFail
							}
						} else if k == 3 {
							connClient.streamid = uint32(id)
							log.Infof("connClient.streamid=%d", connClient.streamid)
						}
					case cmdPublish:
						if int(v.(float64)) != 0 {
							return ErrFail
						}
					}
				case amf.Object:
					objmap := v.(amf.Object)
					switch connClient.curcmdName {
					case cmdConnect:
						code, ok := objmap["code"]
						if ok && code.(string) != connectSuccess {
							return ErrFail
						}
					case cmdPublish:
						code, ok := objmap["code"]
						if ok && code.(string) != publishStart {
							return ErrFail
						}
					}
				}
			}

			return nil
		}
	}
}

func (connClient *ConnClient) writeSubMsg(index int, args ...interface{}) error {
	connClient.bytesw.Reset()
	for _, v := range args {
		if _, err := connClient.encoder.Encode(connClient.bytesw, v, amf.AMF0); err != nil {
			return err
		}
	}
	msg := connClient.bytesw.Bytes()
	c := ChunkStream{
		Format:    0,
		CSID:      3,
		Timestamp: 0,
		TypeID:    20,
		StreamID:  connClient.substreamid[index],
		Length:    uint32(len(msg)),
		Data:      msg,
	}
	connClient.conn.Write(&c)
	return connClient.conn.Flush()
}

func (connClient *ConnClient) WriteTimestampMeta(timestamp uint32) error {
	//log.Printf("WriteTimestampMeta timestamp=%d", timestamp)
	err := connClient.writeMetaDataMsg(connClient.streamid, "syncTimebase", timestamp)
	if err != nil {
		log.Errorf("WriteTimestampMeta error=%v", err)
	}

	return err
}

func (connClient *ConnClient) WriteSubTimestampMeta(index int, timestamp uint32) error {
	if index >= len(connClient.substreamid) {
		log.Errorf("WriteSubTimestampMeta: index(%d) is wrong", index)
		return errors.New(fmt.Sprintf("WriteSubTimestampMeta: index(%d) is wrong", index))
	}
	//log.Printf("WriteSubTimestampMeta index=%d, timestamp=%d", index, timestamp)
	err := connClient.writeMetaDataMsg(connClient.substreamid[index], "syncTimebase", timestamp)
	if err != nil {
		log.Errorf("WriteSubTimestampMeta index=%d, error=%v", index, err)
	}

	return err
}

func (connClient *ConnClient) writeMetaDataMsg(streamid uint32, args ...interface{}) error {
	connClient.bytesw.Reset()
	for _, v := range args {
		if _, err := connClient.encoder.Encode(connClient.bytesw, v, amf.AMF0); err != nil {
			return err
		}
	}
	msg := connClient.bytesw.Bytes()
	c := ChunkStream{
		Format:    0,
		CSID:      3,
		Timestamp: 0,
		TypeID:    18,
		StreamID:  streamid,
		Length:    uint32(len(msg)),
		Data:      msg,
	}
	connClient.conn.Write(&c)
	return connClient.conn.Flush()
}

func (connClient *ConnClient) writeMsg(args ...interface{}) error {
	connClient.bytesw.Reset()
	for _, v := range args {
		if _, err := connClient.encoder.Encode(connClient.bytesw, v, amf.AMF0); err != nil {
			return err
		}
	}
	msg := connClient.bytesw.Bytes()
	c := ChunkStream{
		Format:    0,
		CSID:      3,
		Timestamp: 0,
		TypeID:    20,
		StreamID:  connClient.streamid,
		Length:    uint32(len(msg)),
		Data:      msg,
	}
	connClient.conn.Write(&c)
	return connClient.conn.Flush()
}

func (connClient *ConnClient) writeConnectMsg() error {
	event := make(amf.Object)
	event["app"] = connClient.app
	event["type"] = "nonprivate"
	event["flashVer"] = "FMS.3.1"
	event["tcUrl"] = connClient.tcurl
	connClient.curcmdName = cmdConnect

	log.Infof("writeConnectMsg: connClient.transID=%d, event=%v", connClient.transID, event)
	if err := connClient.writeMsg(cmdConnect, connClient.transID, event); err != nil {
		return err
	}
	return connClient.readRespMsg()
}

func (connClient *ConnClient) writeCreateSubStreamMsg(index int) error {
	connClient.transID++
	connClient.curcmdName = cmdCreateStream

	log.Infof("writeCreateSubStreamMsg: index=%d, connClient.transID=%d", index, connClient.transID)
	if err := connClient.writeSubMsg(index, cmdCreateStream, connClient.transID, nil); err != nil {
		return err
	}

	for {
		err := connClient.readSubRespMsg(index)
		if err == nil {
			return err
		}

		if err == ErrFail {
			log.Errorf("writeCreateSubStreamMsg readRespMsg err=%v", err)
			return err
		}
	}

}

func (connClient *ConnClient) readSubRespMsg(index int) error {
	var err error
	var rc ChunkStream
	for {
		if err = connClient.conn.Read(&rc); err != nil {
			return err
		}
		if err != nil && err != io.EOF {
			return err
		}
		switch rc.TypeID {
		case 20, 17:
			r := bytes.NewReader(rc.Data)
			vs, _ := connClient.decoder.DecodeBatch(r, amf.AMF0)

			log.Infof("readSubRespMsg: vs=%v", vs)
			for k, v := range vs {
				switch v.(type) {
				case string:
					switch connClient.curcmdName {
					case cmdConnect, cmdCreateStream:
						if v.(string) != respResult {
							return errors.New(v.(string))
						}

					case cmdPublish:
						if v.(string) != onStatus {
							return ErrFail
						}
					}
				case float64:
					switch connClient.curcmdName {
					case cmdConnect, cmdCreateStream:
						id := int(v.(float64))

						if k == 1 {
							if id != connClient.transID {
								return ErrFail
							}
						} else if k == 3 {
							connClient.substreamid[index] = uint32(id)
							log.Infof("connClient.substreamid[%d]=%d", index, connClient.substreamid[index])
						}
					case cmdPublish:
						if int(v.(float64)) != 0 {
							return ErrFail
						}
					}
				case amf.Object:
					objmap := v.(amf.Object)
					switch connClient.curcmdName {
					case cmdConnect:
						code, ok := objmap["code"]
						if ok && code.(string) != connectSuccess {
							return ErrFail
						}
					case cmdPublish:
						code, ok := objmap["code"]
						if ok && code.(string) != publishStart {
							return ErrFail
						}
					}
				}
			}

			return nil
		}
	}
}

func (connClient *ConnClient) writeCreateStreamMsg() error {
	connClient.transID++
	connClient.curcmdName = cmdCreateStream

	log.Infof("writeCreateStreamMsg: connClient.transID=%d", connClient.transID)
	if err := connClient.writeMsg(cmdCreateStream, connClient.transID, nil); err != nil {
		return err
	}

	for {
		err := connClient.readRespMsg()
		if err == nil {
			return err
		}

		if err == ErrFail {
			log.Errorf("writeCreateStreamMsg readRespMsg err=%v", err)
			return err
		}
	}

}

func (connClient *ConnClient) writeSubPublishMsg(index int) error {
	connClient.transID++
	connClient.curcmdName = cmdPublish
	log.Infof("writeSubPublishMsg: index=%d, substreamid=%d, transID=%d, title=%s",
		index, connClient.substreamid[index], connClient.transID, connClient.subtitle[index])
	if err := connClient.writeSubMsg(index, cmdPublish, connClient.transID, nil, connClient.subtitle[index], publishLive); err != nil {
		return err
	}
	return connClient.readSubRespMsg(index)
}

func (connClient *ConnClient) writePublishMsg() error {
	connClient.transID++
	connClient.curcmdName = cmdPublish
	if err := connClient.writeMsg(cmdPublish, connClient.transID, nil, connClient.title, publishLive); err != nil {
		return err
	}
	return connClient.readRespMsg()
}

func (connClient *ConnClient) writePlayMsg() error {
	connClient.transID++
	connClient.curcmdName = cmdPlay
	log.Infof("writePlayMsg: connClient.transID=%d, cmdPlay=%v, connClient.title=%v",
		connClient.transID, cmdPlay, connClient.title)

	if err := connClient.writeMsg(cmdPlay, 0, nil, connClient.title); err != nil {
		return err
	}
	return connClient.readRespMsg()
}

func (connClient *ConnClient) StartSubStream(url string, index int, method string) error {
	if connClient.conn == nil {
		return errors.New("the master stream is not connected")
	}

	if index > 1 {
		errString := fmt.Sprintf("the substream index(%d) error", index)
		return errors.New(errString)
	}

	u, err := neturl.Parse(url)
	if err != nil {
		return err
	}
	connClient.suburl[index] = url
	path := strings.TrimLeft(u.Path, "/")
	ps := strings.SplitN(path, "/", 2)
	if len(ps) != 2 {
		return fmt.Errorf("u path err: %s", path)
	}
	connClient.subapp[index] = ps[0]
	connClient.subtitle[index] = ps[1]
	connClient.subquery[index] = u.RawQuery
	connClient.subtcurl[index] = "rtmp://" + u.Host + "/" + connClient.subapp[index]

	log.Infof("StartSubStream:index=%d, url=%s, subapp=%s, subtitle=%s, subquery=%s, subtcurl=%s",
		index, url, connClient.subapp[index], connClient.subtitle[index], connClient.subquery[index], connClient.subtcurl[index])
	if err := connClient.writeCreateSubStreamMsg(index); err != nil {
		log.Errorf("writeCreateStreamMsg error", err)
		return err
	}

	log.Infof("StartSubStream:subindex(%d) method control:%s, %s, %s", index, method, av.PUBLISH, av.PLAY)
	if method == av.PUBLISH {
		if err := connClient.writeSubPublishMsg(index); err != nil {
			log.Errorf("StartSubStream: subindex(%d) writeSubPublishMsg error=%v", index, err)
			return err
		}
	}
	return nil
}

func (connClient *ConnClient) Start(url string, method string) error {
	if connClient.IsStartFlag {
		return errors.New(fmt.Sprintf("ConnClient has already started url=%s", url))
	}
	u, err := neturl.Parse(url)
	if err != nil {
		return err
	}
	connClient.url = url
	path := strings.TrimLeft(u.Path, "/")
	ps := strings.SplitN(path, "/", 2)
	if len(ps) != 2 {
		return fmt.Errorf("u path err: %s", path)
	}
	connClient.app = ps[0]
	connClient.title = ps[1]
	connClient.query = u.RawQuery
	connClient.tcurl = "rtmp://" + u.Host + "/" + connClient.app
	port := ":1935"
	host := u.Host
	localIP := ":0"
	var remoteIP string
	if strings.Index(host, ":") != -1 {
		host, port, err = net.SplitHostPort(host)
		if err != nil {
			return err
		}
		port = ":" + port
	}
	ips, err := net.LookupIP(host)
	log.Infof("ips: %v, host: %v", ips, host)
	if err != nil {
		log.Error(err)
		return err
	}
	remoteIP = ips[rand.Intn(len(ips))].String()
	if strings.Index(remoteIP, ":") == -1 {
		remoteIP += port
	}

	local, err := net.ResolveTCPAddr("tcp", localIP)
	if err != nil {
		log.Error(err)
		return err
	}
	log.Info("remoteIP: ", remoteIP)
	remote, err := net.ResolveTCPAddr("tcp", remoteIP)
	if err != nil {
		log.Error(err)
		return err
	}
	conn, err := net.DialTCP("tcp", local, remote)
	if err != nil {
		log.Error(err)
		return err
	}

	log.Info("connection:", "local:", conn.LocalAddr(), "remote:", conn.RemoteAddr())

	connClient.conn = NewConn(conn, 4*1024)

	log.Info("HandshakeClient....")
	if err := connClient.conn.HandshakeClient(); err != nil {
		return err
	}

	log.Info("writeConnectMsg....")
	if err := connClient.writeConnectMsg(); err != nil {
		return err
	}
	log.Info("writeCreateStreamMsg....")
	if err := connClient.writeCreateStreamMsg(); err != nil {
		log.Error("writeCreateStreamMsg error", err)
		return err
	}

	log.Info("method control:", method, av.PUBLISH, av.PLAY)
	if method == av.PUBLISH {
		if err := connClient.writePublishMsg(); err != nil {
			return err
		}
	} else if method == av.PLAY {
		if err := connClient.writePlayMsg(); err != nil {
			return err
		}
	}

	connClient.IsStartFlag = true
	return nil
}

func (connClient *ConnClient) IsStart() bool {
	return connClient.IsStartFlag
}

func (connClient *ConnClient) Write(c ChunkStream) error {
	if c.TypeID == av.TAG_SCRIPTDATAAMF0 ||
		c.TypeID == av.TAG_SCRIPTDATAAMF3 {
		var err error
		if c.Data, err = amf.MetaDataReform(c.Data, amf.ADD); err != nil {
			return err
		}
		c.Length = uint32(len(c.Data))
	}
	return connClient.conn.Write(&c)
}

func (connClient *ConnClient) Read(c *ChunkStream) (err error) {
	return connClient.conn.Read(c)
}

func (connClient *ConnClient) GetInfo() (app string, name string, url string, conn *Conn) {
	app = connClient.app
	name = connClient.title
	url = connClient.url
	conn = connClient.conn
	return
}

func (connClient *ConnClient) GetStreamId() uint32 {
	return connClient.streamid
}

func (connClient *ConnClient) GetSubStreamId(index int) uint32 {
	return connClient.substreamid[index]
}

func (connClient *ConnClient) Close(err error) {
	connClient.conn.Close()
}
