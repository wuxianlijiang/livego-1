package httpflvclient

import (
	"errors"
	"fmt"
	log "github.com/livego/logging"
	"net"
	"os"
	"strconv"
	"strings"
)

const (
	FLV_ERROR = iota
	RTMP_ERROR
)

type HttpFlvClient struct {
	Url         string
	HostUrl     string
	HostPort    int
	PathUrl     string
	rcvHandle   FlvRcvCallback
	IsStartFlag bool
}

type FlvRcvCallback interface {
	HandleFlvData(data []byte, srcUrl string) error
	StatusReport(stat int)
}

//http://pull2.a8.com/live/1499323853715657.flv
func NewHttpFlvClient(url string) *HttpFlvClient {

	log.Infof("Http flv client %s", url)
	if len(url) <= 6 {
		log.Errorf("url(%s) length(%d) is error", url, len(url))
		return nil
	}

	if url[:7] != "http://" {
		log.Errorf("url(%s) header(%s) is error", url, url[:7])
		return nil
	}
	tempString := url[7:]

	pathArray := strings.Split(tempString, "/")

	hostUrl := pathArray[0]

	hostInfoArray := strings.Split(hostUrl, ":")
	log.Infof("host info array=%v", hostInfoArray)

	var hostPort int
	if len(hostInfoArray) == 1 {
		hostPort = 80
	} else {
		hostUrl = hostInfoArray[0]
		hostportString := hostInfoArray[1]
		var err error
		hostPort, err = strconv.Atoi(hostportString)
		if err != nil {
			log.Errorf("host port(%s) error=%v", hostportString, err)
			return nil
		}
	}
	log.Infof("host url=%s, hostport=%d", hostUrl, hostPort)

	var pathString string
	for _, pachUrl := range pathArray[1:] {
		pathString = pathString + "/" + pachUrl
	}

	//pathString = pathString[0:(len(pathString) - 1)]

	log.Infof("pathurl=%s", pathString)

	return &HttpFlvClient{
		Url:      url,
		HostUrl:  hostUrl,
		HostPort: hostPort,
		PathUrl:  pathString,
	}
}

//only for test use
func checkFileIsExist(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

//only for test use
func WriteFlvFile(data []byte, length int) error {
	filename := "temp.flv"

	ret, err := checkFileIsExist(filename)
	if err != nil {
		return err
	}

	if ret {
		filehandle, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666) //打开文件
		if err != nil {
			log.Errorf("Open file %s error=%v", filename, err)
			return err
		}

		defer filehandle.Close()
		//log.Printf("writeFlvFile(%s): open and write %d bytes", filename, length)
		filehandle.Write(data[:length])
	} else {
		filehandle, err := os.Create(filename)
		if err != nil {
			log.Errorf("Create file %s error=%v", filename, err)
			return err
		}

		defer filehandle.Close()
		//log.Printf("writeFlvFile(%s): create and write %d bytes", filename, length)
		filehandle.Write(data[:length])
	}

	return nil
}

func (self *HttpFlvClient) IsStart() bool {
	return self.IsStartFlag
}

func (self *HttpFlvClient) Start(rcvHandle FlvRcvCallback) error {
	if self.IsStartFlag {
		errString := fmt.Sprintf("HttpFlvClient has already started, url=%s", self.Url)
		log.Error(errString)
		return errors.New(errString)
	}

	hostString := fmt.Sprintf("%s:%d", self.HostUrl, self.HostPort)

	conn, err := net.Dial("tcp", hostString)
	if err != nil {
		log.Errorf("HttpFlvClient.Start(%s) Dail error=%v", hostString, err)
		return err
	}

	content := fmt.Sprintf("GET %s HTTP/1.1\r\n", self.PathUrl)
	content = content + fmt.Sprintf("Accept:*/*\r\n")
	content = content + fmt.Sprintf("Accept-Encoding:gzip\r\n")
	content = content + fmt.Sprintf("Accept-Language:zh_CN\r\n")
	content = content + fmt.Sprintf("Connection:Keep-Alive\r\n")
	content = content + fmt.Sprintf("Host:%s\r\n", self.HostUrl)
	content = content + fmt.Sprintf("Referer:http://www.abc.com/vplayer.swf\r\n\r\n")

	log.Infof("send content:\r\n%s", content)
	conn.Write([]byte(content))

	var rcvBuff []byte

	for {
		temp := make([]byte, 1)
		retLen, err := conn.Read(temp)
		if err != nil || retLen <= 0 {
			log.Errorf("connect read len=%d, error=%v", retLen, err)
			return errors.New("connect read error")
		}
		rcvBuff = append(rcvBuff, temp[0])

		if len(rcvBuff) >= 4 {
			lastIndex := len(rcvBuff) - 1
			if rcvBuff[lastIndex-3] == 0x0d && rcvBuff[lastIndex-2] == 0x0a && rcvBuff[lastIndex-1] == 0x0d && rcvBuff[lastIndex] == 0x0a {
				break
			}
		}
	}
	httpHdrString := string(rcvBuff)
	log.Infof("rcv http header:\r\n%s", httpHdrString)

	index := strings.Index(httpHdrString, "200")
	if index < 0 {
		errString := fmt.Sprintf("http read error:%s", httpHdrString)
		log.Error(errString)
		return errors.New(errString)
	}

	self.rcvHandle = rcvHandle
	self.IsStartFlag = true
	go self.OnRcv(conn)

	return nil
}

func (self *HttpFlvClient) OnRcv(conn net.Conn) {
	const FLV_HEADER_LENGTH = 9
	const RTMP_MESSAGE_HEADER_LENGTH = 15
	const WAIT_FLV_HEADER_STATE = "waiting_for_flvheader"
	const READ_RTMP_HEADER_STATE = "read_rtmpheader"
	const READ_RTMP_BODY_START = "read_rtmpbody"
	currentState := WAIT_FLV_HEADER_STATE

	needLen := FLV_HEADER_LENGTH
	isBreak := false
	log.Infof("rcv data from %s:", self.Url)
	for {
		if !self.IsStartFlag {
			break
		}
		if currentState == WAIT_FLV_HEADER_STATE {
			flvHeaderBuffer := make([]byte, needLen)

			for {
				startPos := FLV_HEADER_LENGTH - needLen
				rcvData := flvHeaderBuffer[startPos:]
				retLen, err := conn.Read(rcvData)
				if err != nil || retLen <= 0 {
					log.Errorf("connect read flv header len=%d, error=%v", retLen, err)
					isBreak = true
					break
				}
				log.Infof("state=WAIT_FLV_HEADER_STATE flv read data:%v", flvHeaderBuffer)
				needLen -= retLen
				if needLen <= 0 {
					currentState = READ_RTMP_HEADER_STATE
					needLen = RTMP_MESSAGE_HEADER_LENGTH

					log.Infof("flv header: %v", flvHeaderBuffer)
					break
				}
			}

		}

		if isBreak {
			break
		}
		var rtmpHeader []byte
		if currentState == READ_RTMP_HEADER_STATE {
			rtmpHeader = make([]byte, needLen)
			for {
				startPos := RTMP_MESSAGE_HEADER_LENGTH - needLen
				rcvData := rtmpHeader[startPos:]
				retLen, err := conn.Read(rcvData)
				if err != nil || retLen <= 0 {
					log.Errorf("connect read rtmp message header len=%d, error=%v", retLen, err)
					isBreak = true
					break
				}
				//log.Infof("state=READ_RTMP_HEADER_STATE flv read data:%v", rtmpHeader)
				needLen -= retLen
				if needLen <= 0 {
					bodyLenByte := rtmpHeader[5:8]
					bodyLen := int(bodyLenByte[0])<<16 + int(bodyLenByte[1])<<8 + int(bodyLenByte[2])
					needLen = bodyLen
					currentState = READ_RTMP_BODY_START
					//log.Printf("rtmp header, bodylen=%d", bodyLen)
					break
				}
			}
		}
		if isBreak {
			break
		}
		var bodyData []byte
		if currentState == READ_RTMP_BODY_START {
			bodyData = make([]byte, needLen)
			bodyLen := needLen
			for {
				startPos := bodyLen - needLen
				rcvData := bodyData[startPos:]
				retLen, err := conn.Read(rcvData)
				if err != nil || retLen <= 0 {
					isBreak = true
					log.Errorf("connect read rtmp body len=%d, error=%v", retLen, err)
					break
				}
				//log.Infof("state=READ_RTMP_BODY_START flv read data:%v", rtmpHeader)
				needLen -= retLen
				if needLen <= 0 {
					//log.Printf("rtmp body(%d)", bodyLen)
					currentState = READ_RTMP_HEADER_STATE
					needLen = RTMP_MESSAGE_HEADER_LENGTH
					break
				}
			}
		}
		if isBreak {
			break
		}
		var rtmppacket []byte
		if len(rtmpHeader) > 0 && len(bodyData) > 0 {
			rtmppacket = append(rtmppacket, rtmpHeader[4:]...)
			rtmppacket = append(rtmppacket, bodyData[:]...)
			if self.IsStartFlag && self.rcvHandle != nil {
				self.rcvHandle.HandleFlvData(rtmppacket, self.Url)
			}
		}
	}

	conn.Close()
	if self.IsStartFlag && self.rcvHandle != nil {
		self.rcvHandle.StatusReport(FLV_ERROR)
	}
}

func (self *HttpFlvClient) Stop() {
	if !self.IsStartFlag {
		log.Errorf("HttpFlvClient has already stoped, url=%s", self.Url)
		return
	}

	self.IsStartFlag = false
	self.rcvHandle = nil
	log.Infof("HttpFlvClient has stoped, url=%s", self.Url)
}
