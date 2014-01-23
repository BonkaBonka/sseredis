# SSE Redis

This is a simple (extremely naïve) Server-Sent Event to Redis PUB/SUB bridge.  It's a mashup of code from:

  * [https://gist.github.com/artisonian/3836281](https://gist.github.com/artisonian/3836281)
  * [https://gist.github.com/tristanwietsma/5486625/](https://gist.github.com/tristanwietsma/5486625/)

[![baby-gopher](https://raw2.github.com/drnic/babygopher-site/gh-pages/images/babygopher-badge.png)](http://www.babygopher.com)

## Sample

	var es = new EventSource('http://localhost:8080/redis/test');
	es.addEventListener('test', function (event) {
		console.log(event);
	}, false);

## License

> The MIT License (MIT)
> 
> Copyright (c) 2013 James Cook
> 
> Permission is hereby granted, free of charge, to any person obtaining a copy of
> this software and associated documentation files (the "Software"), to deal in
> the Software without restriction, including without limitation the rights to
> use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
> the Software, and to permit persons to whom the Software is furnished to do so,
> subject to the following conditions:
> 
> The above copyright notice and this permission notice shall be included in all
> copies or substantial portions of the Software.
> 
> THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
> IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
> FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
> COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
> IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
> CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
