declare module 'bootstra.386/v5.3.1/js/dos.js' {
  const theme: unknown
  export default theme
}

interface Window {
  _386: {
    fastLoad?: boolean
    onePass: boolean
    speedFactor: number
  }
  $: JQueryStatic
  jQuery: JQueryStatic
}
