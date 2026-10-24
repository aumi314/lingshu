import assert from 'node:assert/strict'
import test from 'node:test'
import { parseStandardRoot, recoverStandardRoot, standardBases } from './channelUrls.ts'

test('standard template builds the three protocol bases from an origin', () => {
  assert.deepEqual(standardBases(' https://api.example.com:8443/ '), {
    base_url_chat: 'https://api.example.com:8443/v1',
    base_url_messages: 'https://api.example.com:8443',
    base_url_responses: 'https://api.example.com:8443/v1',
  })
})

test('standard root trims whitespace and trailing slashes', () => {
  assert.deepEqual(parseStandardRoot(' https://api.example.com/// '), { root: 'https://api.example.com' })
})

test('standard root rejects empty, invalid schemes, and paths or query components', () => {
  assert.equal(parseStandardRoot(' ').error, 'required')
  assert.equal(parseStandardRoot('ftp://api.example.com').error, 'invalid')
  assert.equal(parseStandardRoot('https://api.example.com/v1').error, 'path')
  assert.equal(parseStandardRoot('https://api.example.com?tenant=x').error, 'path')
  assert.equal(parseStandardRoot('https://user:pass@api.example.com').error, 'invalid')
})

test('editing a standard channel recovers only matching protocol bases', () => {
  assert.equal(recoverStandardRoot({
    base_url_chat: 'https://api.example.com/v1',
    base_url_messages: 'https://api.example.com',
    base_url_responses: 'https://api.example.com/v1',
  }), 'https://api.example.com')
  assert.equal(recoverStandardRoot({
    base_url_chat: 'https://api.example.com/custom',
    base_url_messages: 'https://api.example.com',
    base_url_responses: 'https://api.example.com/v1',
  }), undefined)
})
