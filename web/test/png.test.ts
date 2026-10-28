import test from 'node:test';
import assert from 'node:assert/strict';
import { encodePNG, validatePNG, WIDTH, HEIGHT, pngChunk } from '../src/png.ts';
import { concat, view } from '../src/bytes.ts';
test('own six-colour indexed PNG validates and rejects CRC/trailing/invalid chunks',async()=>{const png=await encodePNG(new Uint8Array(WIDTH*HEIGHT).fill(4));await validatePNG(png);const tampered=png.slice();tampered[45]^=1;await assert.rejects(validatePNG(tampered));await assert.rejects(validatePNG(concat(png,new Uint8Array([0]))));await assert.rejects(validatePNG(concat(png.subarray(0,33),pngChunk('abca',new Uint8Array()),png.subarray(33))));});
test('PNG with non-six-colour palette entries used by pixels is rejected',async()=>{const png=await encodePNG(new Uint8Array(WIDTH*HEIGHT));const oldSize=view(png).getUint32(33);const palette=new Uint8Array([1,2,3,255,255,255,255,255,0,255,0,0,0,0,255,0,255,0]);const invalid=concat(png.subarray(0,33),pngChunk('PLTE',palette),png.subarray(33+oldSize+12));await assert.rejects(validatePNG(invalid),/像素/);});
