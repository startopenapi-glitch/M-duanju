/** Presentation only. The recharge controller owns all order queries. */
export default function PayQRCode({amount,qr,h5Url}:{amount:number;qr?:string;h5Url?:string}) {
  const safeH5=h5Url && /^https:\/\//i.test(h5Url) ? h5Url : undefined
  return <div className="text-center space-y-3">
    <p>订单金额 <b>¥{amount.toFixed(2)}</b></p>
    {safeH5 ? <a className="btn btn-primary" href={safeH5}>前往微信支付</a> : qr ? <img src={qr} alt="微信支付二维码" width={240} height={240} className="mx-auto max-w-full rounded"/> : <p className="text-sm text-ink2">此处没有可用二维码。若已经付款，请查询原订单，不要重复付款。</p>}
    <p className="text-sm text-ink2">扫码完成后，本页会核对订单和积分到账情况。</p>
  </div>
}
