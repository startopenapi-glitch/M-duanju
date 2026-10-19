/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        paper: '#f6f7f9', card: '#FFFFFF',
        ink: '#162033', ink2: '#3f4b60', ink3: '#566174',
        line: '#dce1e8', line2: '#c4ccd8',
        amber: '#183153', ambersoft: '#edf3fc', amberdeep: '#183153',
        red: '#a63030', redsoft: '#fff0ef', green: '#21623b', greensoft: '#eaf5ee',
        navy: '#183153'
      },
      fontFamily: {
        sans: ['-apple-system','BlinkMacSystemFont','Segoe UI','PingFang SC','Microsoft YaHei','sans-serif'],
        mono: ['SF Mono','Consolas','Liberation Mono','monospace']
      }
    }
  },
  plugins: []
}
