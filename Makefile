.PHONY: buf
buf:
	buf build
	buf generate
	buf generate --template buf.gen.openapi.yaml

.PHONY: update-swagger
update-swagger:
	./hack/update-swagger.sh
