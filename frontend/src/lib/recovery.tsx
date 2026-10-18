import {useEffect,useRef,useSyncExternalStore} from 'react'
import {getToken,clearToken,SESSION_EVENT} from './api'
import {useFocusScope} from '../layout/useFocusScope'
type Recovery={token:string;codes:string[];release:()=>void}
let current:Recovery|null=null
const listeners=new Set<()=>void>()
function emit(){listeners.forEach(fn=>fn())}
function discard(){const old=current;current=null;emit();old?.release()}
export function presentRecovery(codes:string[],release:()=>void){current={token:getToken(),codes,release};emit()}
export function useRecovery(){return useSyncExternalStore(fn=>{listeners.add(fn);return()=>{listeners.delete(fn)}},()=>current,()=>null)}
export function RecoveryNotice(){
 const value=useRecovery(),ref=useRef<HTMLDivElement>(null)
 useFocusScope(ref,!!value)
 useEffect(()=>{const changed=()=>{if(current && current.token!==getToken())discard()};window.addEventListener(SESSION_EVENT,changed);window.addEventListener('storage',changed);return()=>{window.removeEventListener(SESSION_EVENT,changed);window.removeEventListener('storage',changed)}},[])
 useEffect(()=>{if(!value)return;const warn=(e:BeforeUnloadEvent)=>{e.preventDefault();e.returnValue='请先保存恢复码'};window.addEventListener('beforeunload',warn);return()=>window.removeEventListener('beforeunload',warn)},[value])
 if(!value || value.token!==getToken())return null
 return <div className="dlg-backdrop recovery-layer"><div ref={ref} tabIndex={-1} role="dialog" aria-modal="true" aria-labelledby="recovery-heading" className="dlg dlg-body"><h2 id="recovery-heading">请立即保存恢复码</h2><p>恢复码仅显示一次，每个只能使用一次。保存后需要重新登录；验证器登录请使用下一组验证码。</p><div className="grid grid-cols-2 gap-3 font-mono">{value.codes.map(c=><code className="recovery-code" key={c}>{c}</code>)}</div><button className="btn btn-primary" onClick={()=>{const token=value.token;discard();if(getToken()===token)clearToken()}}>我已保存，重新登录</button></div></div>
}
