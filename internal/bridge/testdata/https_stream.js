const assert=require('node:assert/strict'),vm=require('node:vm');
const page=require('node:fs').readFileSync(0,'utf8');
const script=page.match(/<script nonce="[^"]+">([\s\S]*?)<\/script>/)[1];
const context=vm.createContext({assert,AbortController,ReadableStream,URL,setTimeout,clearTimeout,queueMicrotask});
vm.runInContext(`
const messages=[],upChunks=[];
let downController=null,streamOptions=null;
const location={hash:'#android='+'a'.repeat(43),pathname:'/'};
const history={replaceState(){}},addEventListener=()=>{};
const TelegramWebProxy=globalThis.TelegramWebProxy={postMessage(value){messages.push(value)}};
function frame(type,id,payload=new Uint8Array()){
 const data=new Uint8Array(8+payload.length),view=new DataView(data.buffer);
 data.set([type,id>>>16,(id>>>8)&255,id&255]);view.setUint32(4,payload.length);data.set(payload,8);
 return data.buffer;
}
function downEnvelope(cursor,body){
 const raw=new Uint8Array(body),out=new Uint8Array(12+raw.byteLength),view=new DataView(out.buffer);
 view.setBigUint64(0,BigInt(cursor));view.setUint32(8,raw.byteLength);out.set(raw,12);
 return out;
}
const fetch=async(url,opts)=>{
 if(url.endsWith('/api/v1/session')&&opts.method==='POST'){
  return {status:200,headers:{get(name){
   return name==='X-Carrier-Mode'?'https-stream':name==='X-Session-Token'?'session':name==='X-Down-Cursor'?'0':name==='X-Call-Pad-Buckets'?'256,384,512,768':'';
  }},arrayBuffer:async()=>frame(17,0)};
 }
 if(url.endsWith('/api/v1/stream')){
  streamOptions=opts;
  const reader=opts.body.getReader();
  (async()=>{
   while(true){
    const step=await reader.read();
    if(step.done)break;
    upChunks.push(step.value);
   }
  })().catch(()=>{});
  return {status:200,headers:{get(){return ''}},body:new ReadableStream({start(c){downController=c}})};
 }
 return {status:204,headers:{get(){return ''}},arrayBuffer:async()=>new ArrayBuffer(0)};
};
function send(type,id,payload){TelegramWebProxy.onmessage({data:frame(type,id,payload)})}
function healthy(){assert(!messages.some(value=>typeof value==='string'&&JSON.parse(value).state==='failed'))}
`,context);
vm.runInContext(script,context);
vm.runInContext(`
(async()=>{
 send(16,0,new Uint8Array([1]));
 for(let i=0;i<20;i++)await Promise.resolve();
 assert(streamOptions&&streamOptions.duplex==='half','https-stream must set duplex: half on fetch');
 healthy();

 // OpenTCP (type 7) on https-stream must enable call padding on uplink envelopes.
 send(7,5,new Uint8Array([4,149,154,164,10,1,187]));
 send(2,5,new Uint8Array([1,2,3,4]));
 for(let i=0;i<20;i++)await Promise.resolve();
 assert.equal(upChunks.length,2,'uplink envelopes must be enqueued onto ReadableStream');
 for(let idx=0;idx<2;idx++){
  const upView=new DataView(upChunks[idx].buffer,upChunks[idx].byteOffset,upChunks[idx].byteLength);
  assert.equal(upView.getBigUint64(0),BigInt(idx+1),'uplink sequence must increment monotonically');
  assert.equal(upView.getBigUint64(8),0n,'initial ack_cursor must be 0');
  const upBodyLen=upView.getUint32(16);
  assert(upBodyLen===256||upBodyLen===384,'call stream uplink batch on https-stream must be padded to a bucket');
 }

 // Downlink envelope with Pad frame (type 8) must strip Pad and trigger ack-only envelope (sequence=0, ack_cursor=1).
 const d1=new Uint8Array(frame(2,5,new Uint8Array([77]))),p1=new Uint8Array(frame(8,0,new Uint8Array(24)));
 const combined=new Uint8Array(d1.length+p1.length);combined.set(d1,0);combined.set(p1,d1.length);
 const beforeMsg=messages.length;
 downController.enqueue(downEnvelope(1,combined.buffer));
 for(let i=0;i<20;i++)await Promise.resolve();
 healthy();
 const delivered=messages.slice(beforeMsg).filter(v=>v instanceof ArrayBuffer);
 assert.equal(delivered.length,1,'only non-Pad frame must be delivered to native boundary');
 assert.equal(new Uint8Array(delivered[0])[0],2);
 assert.equal(upChunks.length,3,'ack-only uplink envelope must be sent after downlink batch');
 const ackView=new DataView(upChunks[2].buffer,upChunks[2].byteOffset,upChunks[2].byteLength);
 assert.equal(ackView.getBigUint64(0),0n,'ack-only envelope must have sequence 0');
 assert.equal(ackView.getBigUint64(8),1n,'ack-only envelope must carry ack_cursor 1');
 assert.equal(ackView.getUint32(16),0,'ack-only envelope must have length 0');

 TelegramWebProxy.onmessage({data:{t:'close'}});
})()
`,context).catch(error=>{console.error(error);process.exitCode=1});
