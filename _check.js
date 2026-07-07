var fs=require('fs');
var js=fs.readFileSync('_test.js','utf8');
var inStr=false,strChar='';
var depth=0;
var positions=[];
for(var i=0;i<js.length;i++){
  var ch=js[i];
  if(inStr){
    if(ch==='\\'){i++;continue;}
    if(ch===strChar){inStr=false;}
    continue;
  }
  if(ch===String.fromCharCode(39)||ch==='"'||ch==='`'){inStr=true;strChar=ch;continue;}
  if(ch==='{'||ch==='['||ch==='('){depth++;positions.push({pos:i,type:'open',ch:ch});}
  if(ch==='}'||ch===']'||ch===')'){depth--;positions.push({pos:i,type:'close',ch:ch});}
}
console.log('Final depth:',depth);
if(depth>0){
  // 找最后一个未匹配的open
  var d=0;
  for(var i=positions.length-1;i>=0;i--){
    if(positions[i].type==='close')d++;
    if(positions[i].type==='open')d--;
    if(d<0){
      console.log('Unmatched open at position',positions[i].pos,'char:',positions[i].ch);
      console.log('Context:',js.substring(Math.max(0,positions[i].pos-20),positions[i].pos+30));
      break;
    }
  }
}
