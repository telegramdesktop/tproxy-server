const assert=require('node:assert/strict'),vm=require('node:vm');
const page=require('node:fs').readFileSync(0,'utf8');
const script=page.match(/<script nonce="[^"]+">([\s\S]*?)<\/script>/)[1];
const context=vm.createContext({assert,AbortController,URL,setTimeout,clearTimeout,queueMicrotask});
vm.runInContext(`
const sockets=[],messages=[];
const location={hash:'#android='+'a'.repeat(43),pathname:'/'};
const history={replaceState(){}},addEventListener=()=>{};
const TelegramWebProxy=globalThis.TelegramWebProxy={postMessage(value){messages.push(value)}};
function frame(type,id,payload=new Uint8Array()){
 const data=new Uint8Array(8+payload.length),view=new DataView(data.buffer);
 data.set([type,id>>>16,(id>>>8)&255,id&255]);view.setUint32(4,payload.length);data.set(payload,8);
 return data.buffer;
}
const fetch=async()=>({status:200,headers:{get(name){
 return name==='X-Carrier-Mode'?'websocket-lanes':name==='X-Session-Token'?'session':name==='X-Call-Pad-Buckets'?'256,384,512,768':'';
}},arrayBuffer:async()=>frame(17,0)});
class WebSocket{
 static CONNECTING=0;static OPEN=1;static CLOSED=3;
 constructor(url,protocol){this.readyState=0;this.bufferedAmount=0;this.sent=[];sockets.push(this)}
 send(value){assert.equal(this.readyState,1);this.sent.push(value)}
 close(){this.readyState=3;this.onclose?.()}
 open(){this.readyState=1;this.onopen?.()}
}
function send(type,id,payload){TelegramWebProxy.onmessage({data:frame(type,id,payload)})}
function healthy(){assert(!messages.some(value=>typeof value==='string'&&JSON.parse(value).state==='failed'))}
`,context);
vm.runInContext(script,context);
vm.runInContext(`
(async()=>{
 send(16,0,new Uint8Array([1]));
 for(let i=0;i<10;i++)await Promise.resolve();
 assert(messages.some(value=>value instanceof ArrayBuffer&&new Uint8Array(value)[0]===17));

 send(1,1);send(2,1,new Uint8Array([42]));
 const canceled=sockets[0];
 send(3,1);
 assert.equal(canceled.readyState,WebSocket.CLOSED,'CLOSE must abort a queued WebSocket handshake');
 assert.equal(canceled.sent.length,0,'abandoned OPEN and DATA must never reach the server');
 canceled.open();
 assert.equal(canceled.readyState,WebSocket.CLOSED,'late open must not resurrect a canceled lane');
 healthy();

 // Exceed the shared byte and item budgets cumulatively. Cancellation must
 // release each reservation, including when close invokes onclose inline.
 const payload=new Uint8Array(8192);
 for(let id=2;id<=9001;id++){
  send(1,id);send(2,id,payload);send(3,id);
  assert.equal(sockets.at(-1).readyState,WebSocket.CLOSED);
 }
 healthy();

 send(1,9002);send(2,9002,new Uint8Array([7]));
 const live=sockets.at(-1);live.open();
 assert.equal(live.sent.length,1,'live lane must flush its queued frames');
 assert.equal(new Uint8Array(live.sent[0])[0],1);
 assert.equal(live.sent[0].byteLength,17,'non-call lane must not be padded');
 send(3,9002);
 assert.equal(live.readyState,WebSocket.OPEN,'established lane must send its protocol CLOSE');
 assert.equal(new Uint8Array(live.sent.at(-1))[0],3);
 live.close();
 const count=sockets.length;
 send(2,9002,payload);send(3,9002);send(4,9002,new Uint8Array(4));
 assert.equal(sockets.length,count,'late frames must not reopen a finished lane');
 healthy();

 // OpenTCP (type 7) must open a call lane, pad uplink writes to a bucket,
 // and strip downlink Pad (type 8, stream 0) frames before native postMessage.
 send(7,9003,new Uint8Array([4,149,154,164,10,1,187]));
 send(2,9003,new Uint8Array([99]));
 const callLane=sockets.at(-1);callLane.open();
 assert.equal(callLane.sent.length,1,'OpenTCP lane must flush its queued frames');
 assert.equal(new Uint8Array(callLane.sent[0])[0],7);
 assert(callLane.sent[0].byteLength===256||callLane.sent[0].byteLength===384,'call lane uplink must be padded to a bucket');
 const downData=new Uint8Array(frame(2,9003,new Uint8Array([55]))),downPad=new Uint8Array(frame(8,0,new Uint8Array(32)));
 const downBatch=new Uint8Array(downData.length+downPad.length);
 downBatch.set(downData,0);downBatch.set(downPad,downData.length);
 const beforeMessages=messages.length;
 callLane.onmessage({data:downBatch.buffer});
 healthy();
 const binaryDeliveries=messages.slice(beforeMessages).filter(v=>v instanceof ArrayBuffer);
 assert.equal(binaryDeliveries.length,1,'only the DATA frame must reach the native boundary');
 assert.equal(new Uint8Array(binaryDeliveries[0])[0],2,'Pad frame must be stripped before native delivery');
 callLane.close();
 healthy();

 TelegramWebProxy.onmessage({data:{t:'close'}});
})()
`,context).catch(error=>{console.error(error);process.exitCode=1});
