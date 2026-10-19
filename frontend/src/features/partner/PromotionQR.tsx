import { useEffect, useState } from 'react'
import QRCode from 'qrcode'
export function PromotionQR({ value }: { value: string }) {
  const [url, setURL] = useState(''),
    [error, setError] = useState(''),
    [attempt, setAttempt] = useState(0)
  useEffect(() => {
    let active = true
    setURL('')
    setError('')
    QRCode.toString(value, {
      type: 'svg',
      width: 240,
      margin: 4,
      errorCorrectionLevel: 'M',
      color: { dark: '#1b2a4a', light: '#ffffff' },
    })
      .then((data) => {
        if (active)
          setURL(`data:image/svg+xml;charset=utf-8,${encodeURIComponent(data)}`)
      })
      .catch(() => {
        if (active) setError('二维码生成失败')
      })
    return () => {
      active = false
    }
  }, [value, attempt])
  return (
    <div className="ws-qr">
      {url ? (
        <>
          <img src={url} width="192" height="192" alt="推广链接二维码" />
          <a className="btn" href={url} download="短剧工厂-推广二维码.svg">
            下载二维码
          </a>
        </>
      ) : error ? (
        <div role="alert">
          {error}
          <button className="btn" onClick={() => setAttempt((n) => n + 1)}>
            重新生成二维码
          </button>
        </div>
      ) : (
        <p role="status">正在本地生成二维码…</p>
      )}
    </div>
  )
}
