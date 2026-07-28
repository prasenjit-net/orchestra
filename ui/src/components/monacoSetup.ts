import { loader } from '@monaco-editor/react'
import * as monaco from 'monaco-editor'
import editorWorker from 'monaco-editor/esm/vs/editor/editor.worker?worker'
import cssWorker from 'monaco-editor/esm/vs/language/css/css.worker?worker'
import htmlWorker from 'monaco-editor/esm/vs/language/html/html.worker?worker'
import jsonWorker from 'monaco-editor/esm/vs/language/json/json.worker?worker'
import typeScriptWorker from 'monaco-editor/esm/vs/language/typescript/ts.worker?worker'

type MonacoEnvironment = {
  getWorker: (_moduleID: string, label: string) => Worker
}

const monacoGlobal = self as typeof globalThis & {
  MonacoEnvironment?: MonacoEnvironment
}

monacoGlobal.MonacoEnvironment = {
  getWorker(_moduleID, label) {
    if (label === 'json') return new jsonWorker()
    if (label === 'css' || label === 'scss' || label === 'less') return new cssWorker()
    if (label === 'html' || label === 'handlebars' || label === 'razor') return new htmlWorker()
    if (label === 'typescript' || label === 'javascript') return new typeScriptWorker()
    return new editorWorker()
  },
}

loader.config({ monaco })
