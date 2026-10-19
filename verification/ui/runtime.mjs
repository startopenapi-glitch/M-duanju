import {pathToFileURL} from 'node:url';
import assert from 'node:assert/strict';
export function args(){const a=process.argv.slice(2);return Object.fromEntries(a.reduce((r,k,i)=>k.startsWith('--')?[...r,[k.slice(2),a[i+1]]]:r,[]));}
export function localBase(base){const u=new URL(base);assert.ok(['localhost','127.0.0.1','[::1]'].includes(u.hostname),'loopback origin required');return u.origin;}
export async function launch(){const m=process.env.PLAYWRIGHT_MODULE;const {chromium}=await import(m?pathToFileURL(m).href:'playwright');return chromium.launch({headless:true,...(process.env.CHROMIUM_EXECUTABLE?{executablePath:process.env.CHROMIUM_EXECUTABLE}:{})});}
