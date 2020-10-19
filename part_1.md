图说比特币
======


# 引言

这个系列使用**golang**从零开始写一个**比特币轻量化节点**。最终达到和实际的比特币网络进行交易和**SPV**(Simplified Payment Verification)。

注意：大部分代码都源自[BTCD](https://github.com/btcsuite/btcd)，实际上也是一个**[BTCD](https://github.com/btcsuite/btcd)源码分析**文章。最终实现以下功能：
1. 连接到比特币的网络(包括 mainnet, testnet, simnet)
2. 加入到比特币网络(“version handshake”)
3. 向其他节点获取blockchain state
4. 下载blockhain heads
5. 实现**SPV**(Simplified Payment Verification)
6. **交易**比特币

**注：阅读前请确认已经对比特币的区块链结构已经有初步的了解。**

## 搭建测试网络

编程初期我们使用BCTD自带的测试网络。测试网络的配置如下:
```
# btcd.conf
[Application Options]
datadir=./btcd/data

listen=127.0.0.1:9333

simnet=1

nobanning=1
debuglevel=debug
```

1. `listen` 是测试网络的ip
2. `simnet` 代表模拟网络(Simulation Network)
3. `nobanning` 不要禁止任何节点
4. `debuglevel=debug` 输出日志信息

启动终端输入命令： `btcd --configfile ./btcd.conf` 即可启动测试网络。



## 通信协议

追求妹子的第一步是了解对方想要什么，比如刚认识的时候首先看看颜值，颜值不行的基本就是冷处理备胎好人卡三连了。节点间通信也是一样，一开始的时候先看看版本，版本不行的也是被拒绝的下场。

```go
// #msgversion.go
type MsgVersion struct {
	// Version of the protocol the node is using.
	ProtocolVersion int32

	// Bitfield which identifies the enabled services.
	Services ServiceFlag

	// Time the message was generated.  This is encoded as an int64 on the wire.
	Timestamp time.Time

	// Address of the remote peer.
	AddrYou NetAddress

	// Address of the local peer.
	AddrMe NetAddress

	// Unique value associated with message that is used to detect self
	// connections.
	Nonce uint64

	// The user agent that generated messsage.  This is a encoded as a varString
	// on the wire.  This has a max length of MaxUserAgentLen.
	UserAgent string

	// Last block seen by the generator of the version message.
	LastBlock int32

	// Don't announce transactions to peer.
	DisableRelayTx bool
}

```

1. `ProtocolVersion`表示协议版本，版本号越大则代表版本越新。详见[Protocol Versions](https://bitcoin.org/en/developer-reference#protocol-versions)

2. `Services` 代表节点支持的服务。暂时使用`1`表示全节点
3. `UserAgent` 类似于http协议的`User-Agent` ，包含节点软件的名称和版本
4. `LastBlock` 代表最新区块的高度


`MsgVersion`代表了需要发送的**内容**。
由于消息的底层是**字节序列**,所有我们需要约定好字节序列的格式。

```go
// #message.go
type messageHeader struct {
	magic    BitcoinNet // 4 bytes
	command  string     // 12 bytes
	length   uint32     // 4 bytes
	checksum [4]byte    // 4 bytes
}
```

1. `magic` 表示网络类型，目前用`SimNet`表示测试网络。
2. `command` 表示命令名称，目前用`CmdVersion`表示这是获取版本的命令。
3. `length` 表示**数据内容**的长度。
4. `checksum` **数据内容**的双重哈希。

**数据内容**的获取接口为下方的`BtcEncode`。
**数据内容的最大长度**的获取接口为下方的`MaxPayloadLength`。
**命令名称**的获取接口为下方的`Command`。
```go
// #message.go
type Message interface {
	BtcDecode(io.Reader, uint32, MessageEncoding) error
	BtcEncode(io.Writer, uint32, MessageEncoding) error
	Command() string
	MaxPayloadLength(uint32) uint32
}
```

最终发送的消息字节序列格式：
```
BYTES(Msg.magic) + BYTES(Msg.Command()) + BYTES(Msg.length) + BYTES(Msg.checksum) + BYTES(Msg.BtcEncode())
```

## 将消息转化为字节序列
`Msg.BtcEncode()` 实现如下：
```go
// #msgversion.go
// BtcEncode encodes the receiver to w using the bitcoin protocol encoding.
// This is part of the Message interface implementation.
func (msg *MsgVersion) BtcEncode(w io.Writer, pver uint32, enc MessageEncoding) error {
	err := validateUserAgent(msg.UserAgent)
	if err != nil {
		return err
	}

	err = writeElements(w, msg.ProtocolVersion, msg.Services,
		msg.Timestamp.Unix())
	if err != nil {
		return err
	}

	err = writeNetAddress(w, pver, &msg.AddrYou, false)
	if err != nil {
		return err
	}

	err = writeNetAddress(w, pver, &msg.AddrMe, false)
	if err != nil {
		return err
	}

	err = writeElement(w, msg.Nonce)
	if err != nil {
		return err
	}

	err = WriteVarString(w, pver, msg.UserAgent)
	if err != nil {
		return err
	}

	err = writeElement(w, msg.LastBlock)
	if err != nil {
		return err
	}

	// There was no relay transactions field before BIP0037Version.  Also,
	// the wire encoding for the field is true when transactions should be
	// relayed, so reverse it from the DisableRelayTx field.
	if pver >= BIP0037Version {
		err = writeElement(w, !msg.DisableRelayTx)
		if err != nil {
			return err
		}
	}
	return nil
}

```
`Msg.Command()` 实现如下：
```go
// #msgversion.go
// Command returns the protocol command string for the message.  This is part
// of the Message interface implementation.
func (msg *MsgVersion) Command() string {
	return CmdVersion
}
```
`Msg.MaxPayloadLength()` 实现如下：
```go
// #msgversion.go
// MaxPayloadLength returns the maximum length the payload can be for the
// receiver.  This is part of the Message interface implementation.
func (msg *MsgVersion) MaxPayloadLength(pver uint32) uint32 {
	// XXX: <= 106 different

	// Protocol version 4 bytes + services 8 bytes + timestamp 8 bytes +
	// remote and local net addresses + nonce 8 bytes + length of user
	// agent (varInt) + max allowed useragent length + last block 4 bytes +
	// relay transactions flag 1 byte.
	return 33 + (maxNetAddressPayload(pver) * 2) + MaxVarIntPayload +
		MaxUserAgentLen
}

```

最后实现各个序列的整合：
```
BYTES(Msg.magic) + BYTES(Msg.Command()) + BYTES(Msg.length) + BYTES(Msg.checksum) + BYTES(Msg.BtcEncode())
```

```go
// #message.go
// WriteMessageWithEncodingN writes a bitcoin Message to w including the
// necessary header information and returns the number of bytes written.
// This function is the same as WriteMessageN except it also allows the caller
// to specify the message encoding format to be used when serializing wire
// messages.
func WriteMessageWithEncodingN(w io.Writer, msg Message, pver uint32,
	btcnet BitcoinNet, encoding MessageEncoding) (int, error) {

	totalBytes := 0

	// Enforce max command size.
	var command [CommandSize]byte
	cmd := msg.Command()
	if len(cmd) > CommandSize {
		str := fmt.Sprintf("command [%s] is too long [max %v]",
			cmd, CommandSize)
		return totalBytes, messageError("WriteMessage", str)
	}
	copy(command[:], []byte(cmd))

	// Encode the message payload.
	var bw bytes.Buffer
	err := msg.BtcEncode(&bw, pver, encoding)
	if err != nil {
		return totalBytes, err
	}
	payload := bw.Bytes()
	lenp := len(payload)

	// Enforce maximum overall message payload.
	if lenp > MaxMessagePayload {
		str := fmt.Sprintf("message payload is too large - encoded "+
			"%d bytes, but maximum message payload is %d bytes",
			lenp, MaxMessagePayload)
		return totalBytes, messageError("WriteMessage", str)
	}

	// Enforce maximum message payload based on the message type.
	mpl := msg.MaxPayloadLength(pver)
	if uint32(lenp) > mpl {
		str := fmt.Sprintf("message payload is too large - encoded "+
			"%d bytes, but maximum message payload size for "+
			"messages of type [%s] is %d.", lenp, cmd, mpl)
		return totalBytes, messageError("WriteMessage", str)
	}

	// Create header for the message.
	hdr := messageHeader{}
	hdr.magic = btcnet
	hdr.command = cmd
	hdr.length = uint32(lenp)
	copy(hdr.checksum[:], chainhash.DoubleHashB(payload)[0:4])

	// Encode the header for the message.  This is done to a buffer
	// rather than directly to the writer since writeElements doesn't
	// return the number of bytes written.
	hw := bytes.NewBuffer(make([]byte, 0, MessageHeaderSize))
	writeElements(hw, hdr.magic, command, hdr.length, hdr.checksum)

	// Write header.
	n, err := w.Write(hw.Bytes())
	totalBytes += n
	if err != nil {
		return totalBytes, err
	}

	// Only write the payload if there is one, e.g., verack messages don't
	// have one.
	if len(payload) > 0 {
		n, err = w.Write(payload)
		totalBytes += n
	}

	return totalBytes, err
}

```


## 发送Version消息

我们要做以下的事情：
1. 连接到`BCTD`的测试网络
2. 发送`Version`消息
3. 打印返回的消息


注意：以下代码不是来自BCTD，仅用于当前版本测试使用。
```go
// #version.go

nodeURL := "127.0.0.1:9333"
// Create version message data.
lastBlock := int32(234234)
tcpAddrMe := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8333}
me := wire.NewNetAddress(tcpAddrMe, wire.SFNodeNetwork)
tcpAddrYou := &net.TCPAddr{IP: net.ParseIP("192.168.0.1"), Port: 8333}
you := wire.NewNetAddress(tcpAddrYou, wire.SFNodeNetwork)
nonce, err := wire.RandomUint64()
if err != nil {
    fmt.Printf("RandomUint64: error generating nonce: %v", err)
}

// Ensure we get the correct data back out.
msg := wire.NewMsgVersion(me, you, nonce, lastBlock)
msg.AddService(wire.SFNodeNetwork)
conn, err := net.Dial("tcp", nodeURL)
if err != nil {
    logrus.Fatalln(err)
}
defer conn.Close()

p := peer.NewPeerTemp(conn)
err = p.WriteMessage(msg, wire.LatestEncoding)
if err != nil {
    logrus.Fatalln(err)
}

tmp := make([]byte, 256)

for {
    n, err := conn.Read(tmp)
    if err != nil {
        if err != io.EOF {
logrus.Fatalln(err)
        }
        return
    }
    logrus.Infof("received: %x", tmp[:n])
}
	
```

运行命令：

`go run ./`

结果如下：
```
126
INFO[0000] received: 161c141276657273696f6e0000000000710000003b6dddb97d1101004d0000000000000017b16a5f000000004d0000000000000000000000000000000000ffff7f000001c02b4d000000000000000000000000000000000000000000000000004f8b69631962b8121b2f627463776972653a302e352e302f627463643a302e32302e312f0000000001 
INFO[0000] received: 161c141276657261636b000000000000000000005df6e0e2 
```

## 总结：

到这里本文就结束了。下一篇我们会解析返回的消息。




参考资料：
[BTCD](https://github.com/btcsuite/btcd)
[比特币消息协议](https://en.bitcoin.it/wiki/Protocol_documentation#version)
[Tinybit](https://github.com/Jeiwan/tinybit/tree/part_1)
《比特币白皮书》
《解构区块链》
















