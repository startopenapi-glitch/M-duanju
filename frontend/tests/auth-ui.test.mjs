import test from 'node:test'
import assert from 'node:assert/strict'
import { build } from 'esbuild'
import { createRequire } from 'node:module'
import { fileURLToPath } from 'node:url'
import { renderToStaticMarkup } from 'react-dom/server'

const require = createRequire(import.meta.url)
async function load(entry) {
 const result=await build({entryPoints:[fileURLToPath(new URL(entry,import.meta.url))],bundle:true,write:false,platform:'node',format:'cjs',packages:'external',jsx:'automatic',loader:{'.css':'empty'}})
 const module={exports:{}}
 new Function('require','module','exports',result.outputFiles[0].text)(require,module,module.exports)
 return module.exports
}
function findInput(element) {
 if (!element || typeof element!=='object') return null
 if (element.type==='input') return element
 for(const child of [element.props?.children].flat(Infinity)){const found=findInput(child);if(found)return found}
 return null
}
test('recovery-code entry preserves all ten typed or pasted characters',async()=>{
 const mod=await load('../src/pages/Login.tsx')
 assert.equal(typeof mod.SecondFactorInput,'function','login needs an explicit recovery-code input')
 let value
 const element=mod.SecondFactorInput({kind:'recovery',value:'',onChange:v=>{value=v}})
 const input=findInput(element)
 assert.equal(input.props.maxLength,10)
 input.props.onChange({target:{value:' ab12 cd34ef '}})
 assert.equal(value,'AB12CD34EF')
 const html=renderToStaticMarkup(element)
 assert.match(html,/inputMode="text"/i)
})
test('authenticator entry accepts only six digits',async()=>{
 const mod=await load('../src/pages/Login.tsx')
 assert.equal(typeof mod.SecondFactorInput,'function')
 let value
 const input=findInput(mod.SecondFactorInput({kind:'totp',value:'',onChange:v=>{value=v}}))
 assert.equal(input.props.maxLength,6)
 input.props.onChange({target:{value:'12 a34-567'}})
 assert.equal(value,'123456')
})
test('security QR component accepts local PNG data and rejects external image URLs',async()=>{
 const mod=await load('../src/pages/Security.tsx')
 assert.equal(typeof mod.TotpQRCode,'function','TOTP setup must render a local QR')
 const html=renderToStaticMarkup(mod.TotpQRCode({dataUrl:'data:image/png;base64,aGVsbG8='}))
 assert.match(html,/src="data:image\/png;base64,/)
 assert.doesNotMatch(renderToStaticMarkup(mod.TotpQRCode({dataUrl:'https://thirdparty.invalid/?secret=synthetic'})),/<img/)
})
test('email-code needTotp is preserved as structured API error and leaves an existing token intact',async()=>{
 globalThis.localStorage={getItem:()=> 'synthetic-token',removeItem:()=>assert.fail('login credential error cleared token')}
 globalThis.location={pathname:'/login',href:''}
 globalThis.fetch=async()=>new Response(JSON.stringify({ok:false,error:'second factor required',needTotp:true}),{status:401})
 const {api}=await load('../src/lib/api.ts')
 await assert.rejects(api.post('/auth/login-by-code',{email:'person@example.test',code:'123456'}),err=>err.needTotp===true && err.status===401)
})
