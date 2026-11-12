FROM node:22-alpine
WORKDIR /app
RUN npm install yarn
COPY package.json yarn.lock ./
RUN yarn install

COPY . .
EXPOSE 5173
ENTRYPOINT ["yarn", "dev"]
